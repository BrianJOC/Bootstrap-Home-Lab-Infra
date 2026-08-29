packer {
  required_plugins {
    qemu = {
      source  = "github.com/hashicorp/qemu"
      version = ">= 1.0.0"
    }
  }
}

variable "ubuntu_version" {
  # Ubuntu LTS release. Cloud image URLs use the short form (e.g. "24.04").
  default = "24.04"
}

variable "output_dir" {
  default = "test/images/output"
}

# Target guest architecture: x86_64 or aarch64.
# Default: aarch64 (Apple Silicon). Override for Linux CI: -var 'arch=x86_64'
variable "arch" {
  default = "aarch64"
}

# QEMU accelerator: hvf on macOS, kvm on Linux.
# Override for Linux CI: -var 'accelerator=kvm'
variable "accelerator" {
  default = "hvf"
}

# Path to aarch64 UEFI firmware. Only used when arch=aarch64.
# macOS (Homebrew): /opt/homebrew/share/qemu/edk2-aarch64-code.fd
# Linux: /usr/share/qemu/OVMF.fd or /usr/share/OVMF/OVMF_CODE.fd
variable "uefi_firmware" {
  default = "/opt/homebrew/share/qemu/edk2-aarch64-code.fd"
}

locals {
  is_arm      = var.arch == "aarch64"
  arch_suffix = local.is_arm ? "arm64" : "amd64"
  qemu_binary = local.is_arm ? "qemu-system-aarch64" : "qemu-system-x86_64"
  machine_type = local.is_arm ? "virt" : "q35"
  output_name  = "base-${var.arch}"

  # Ubuntu publishes cloud images (pre-installed, cloud-init ready) at a stable
  # URL per LTS release. Packer caches the download by checksum.
  cloud_image_url = "https://cloud-images.ubuntu.com/releases/${var.ubuntu_version}/release/ubuntu-${var.ubuntu_version}-server-cloudimg-${local.arch_suffix}.img"
  checksum_url    = "file:https://cloud-images.ubuntu.com/releases/${var.ubuntu_version}/release/SHA256SUMS"

  # When qemuargs contains any -drive entry, Packer suppresses its own disk
  # management entirely. So the primary disk must be specified here explicitly.
  # Packer creates the output qcow2 (copy + resize of the cloud image) before
  # launching QEMU, so the path is valid when QEMU starts.
  #
  # The seed ISO uses virtio-scsi rather than a plain -drive media=cdrom so it
  # presents as a proper SCSI CD-ROM that cloud-init's blkid can identify.
  # xorriso produces plain ISO 9660 (vs hdiutil's HFS+ hybrid) so Linux blkid
  # correctly reads the "cidata" volume label and NoCloud datasource fires.
  qemuargs = local.is_arm ? [
    ["-bios", var.uefi_firmware],
    ["-cpu", "host"],
    ["-boot", "menu=off"],
    ["-device", "virtio-gpu-pci"],
    ["-drive", "file=${var.output_dir}/${local.output_name}.qcow2,if=virtio,cache=writeback,discard=ignore,format=qcow2"],
    ["-drive", "file=test/images/seed.iso,media=cdrom,readonly=on,if=none,id=cidata"],
    ["-device", "virtio-scsi-pci,id=scsi0"],
    ["-device", "scsi-cd,bus=scsi0.0,drive=cidata"],
  ] : [
    ["-drive", "file=${var.output_dir}/${local.output_name}.qcow2,if=virtio,cache=writeback,discard=ignore,format=qcow2"],
    ["-drive", "file=test/images/seed.iso,media=cdrom,readonly=on,if=none,id=cidata"],
    ["-device", "virtio-scsi-pci,id=scsi0"],
    ["-device", "scsi-cd,bus=scsi0.0,drive=cidata"],
  ]
}

source "qemu" "base" {
  # disk_image = true: iso_url is treated as the boot disk, not an installer ISO.
  # Packer converts and resizes it to the output qcow2. The VM boots directly
  # into the pre-installed Ubuntu system; no OS installer runs.
  iso_url      = local.cloud_image_url
  iso_checksum = local.checksum_url
  disk_image   = true

  output_directory = var.output_dir
  vm_name          = "${local.output_name}.qcow2"
  # GitLab Omnibus needs several GiB of temporary package/unpack headroom in
  # addition to the rest of the disposable platform stack.
  disk_size        = "30G"
  format           = "qcow2"
  accelerator      = var.accelerator
  machine_type     = local.machine_type
  qemu_binary      = local.qemu_binary
  memory           = 4096
  cpus             = 2

  ssh_username         = "ansible"
  ssh_private_key_file = "test/keys/test_id_ed25519"
  # Cloud-init typically completes in under 2 minutes. 10m is generous.
  ssh_timeout = "10m"

  headless  = true
  boot_wait = "5s"

  shutdown_command = "echo 'packer' | sudo -S shutdown -P now"
  qemuargs         = local.qemuargs
}

build {
  sources = ["source.qemu.base"]

  # Install Ansible prerequisites. Cloud images ship with python3 but
  # python3-apt is needed for the apt module.
  provisioner "shell" {
    inline = [
      "sudo apt-get update -qq",
      "sudo apt-get install -y -qq python3 python3-apt",
    ]
  }
}
