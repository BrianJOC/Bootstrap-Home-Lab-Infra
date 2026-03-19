packer {
  required_plugins {
    qemu = {
      source  = "github.com/hashicorp/qemu"
      version = ">= 1.0.0"
    }
  }
}

variable "ubuntu_version" {
  default = "22.04"
}

variable "output_dir" {
  default = "test/images"
}

variable "arch" {
  default = "x86_64"  # or aarch64
}

locals {
  ubuntu_iso_url = var.arch == "x86_64" ? "https://releases.ubuntu.com/${var.ubuntu_version}/ubuntu-${var.ubuntu_version}-live-server-amd64.iso" : "https://cdimage.ubuntu.com/releases/${var.ubuntu_version}/release/ubuntu-${var.ubuntu_version}-live-server-arm64.iso"
  output_name    = "base-${var.arch}"
}

source "qemu" "base" {
  iso_url          = local.ubuntu_iso_url
  iso_checksum     = "file:${local.ubuntu_iso_url}.sha256"  # use file checksum
  output_directory = var.output_dir
  vm_name          = "${local.output_name}.qcow2"
  disk_size        = "10G"
  format           = "qcow2"
  accelerator      = "kvm"
  memory           = 2048
  cpus             = 2
  ssh_username     = "ansible"
  ssh_private_key_file = "test/keys/test_id_ed25519"
  ssh_timeout      = "30m"
  headless         = true
  boot_wait        = "5s"
  boot_command     = [
    "<enter><wait>",
    "e<wait>",
    "<down><down><down><end>",
    " autoinstall ds=nocloud-net;seedfrom=http://{{.HTTPIP}}:{{.HTTPPort}}/",
    "<f10>"
  ]
  http_directory   = "test/images/http"
  shutdown_command = "echo 'packer' | sudo -S shutdown -P now"
}

build {
  sources = ["source.qemu.base"]

  provisioner "shell" {
    inline = [
      "sudo apt-get update -qq",
      "sudo apt-get install -y -qq python3 python3-apt openssh-server",
      "sudo systemctl enable ssh"
    ]
  }

  provisioner "file" {
    source      = "test/keys/test_id_ed25519.pub"
    destination = "/tmp/test_key.pub"
  }

  provisioner "shell" {
    inline = [
      "sudo mkdir -p /home/ansible/.ssh",
      "sudo cat /tmp/test_key.pub | sudo tee /home/ansible/.ssh/authorized_keys",
      "sudo chmod 700 /home/ansible/.ssh",
      "sudo chmod 600 /home/ansible/.ssh/authorized_keys",
      "sudo chown -R ansible:ansible /home/ansible/.ssh"
    ]
  }
}
