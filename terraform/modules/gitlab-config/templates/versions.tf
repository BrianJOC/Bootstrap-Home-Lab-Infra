terraform {
  # The shell runner itself is pinned, while the generated project remains
  # validation-compatible with newer Terraform 1.x developer tooling.
  required_version = ">= 1.6.6, < 2.0.0"

  required_providers {
    maas = {
      source  = "canonical/maas"
      version = "2.8.0"
    }
  }

  backend "http" {}
}

provider "maas" {
  api_version = "2.0"
}
