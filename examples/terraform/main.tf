# Test Terraform/OpenTofu code against a throwaway cluster with the bpg/proxmox provider.
#
#   proxbase create -f examples/single-node.yaml
#   proxbase env single --format terraform > provider.tf
#   tofu init && tofu apply      # or terraform
#   proxbase destroy single --yes
terraform {
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = ">= 0.80.0"
    }
  }
}

variable "node" {
  type    = string
  default = "pve1"
}

# A small VM without disks or OS: enough to test create, update and destroy.
resource "proxmox_virtual_environment_vm" "test" {
  name      = "tf-test"
  node_name = var.node
  started   = false

  cpu {
    cores = 1
  }
  memory {
    dedicated = 512
  }
}

output "vm_id" {
  value = proxmox_virtual_environment_vm.test.vm_id
}
