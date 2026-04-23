#!/usr/bin/env bash
set -euo pipefail

# Runs a Linux kernel under QEMU and validates the kernel 9p client by
# mounting a virtio-9p export (QEMU's built-in server) and executing a
# small test binary inside the guest.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/.kernel9p-out}"

KERNEL_BZIMAGE="${KERNEL_BZIMAGE:-${OUT_DIR}/linux/arch/x86/boot/bzImage}"
INITRAMFS_GZ="${INITRAMFS_GZ:-${OUT_DIR}/initramfs.cpio.gz}"
SHARE_DIR="${SHARE_DIR:-${OUT_DIR}/share}"

QEMU_BIN="${QEMU_BIN:-qemu-system-x86_64}"

mkdir -p "${OUT_DIR}" "${SHARE_DIR}"

if [[ ! -f "${KERNEL_BZIMAGE}" ]]; then
  echo "Missing kernel bzImage at ${KERNEL_BZIMAGE}" >&2
  exit 2
fi
if [[ ! -f "${INITRAMFS_GZ}" ]]; then
  echo "Missing initramfs at ${INITRAMFS_GZ}" >&2
  exit 2
fi

echo "Starting QEMU kernel9p test..."

"${QEMU_BIN}" \
  -nodefaults \
  -no-reboot \
  -m 1024 \
  -cpu max \
  -machine q35 \
  -serial mon:stdio \
  -nographic \
  -kernel "${KERNEL_BZIMAGE}" \
  -initrd "${INITRAMFS_GZ}" \
  -append "console=ttyS0 panic=1 oops=panic loglevel=7" \
  -device virtio-rng-pci \
  -fsdev local,id=fsdev0,path="${SHARE_DIR}",security_model=none \
  -device virtio-9p-pci,fsdev=fsdev0,mount_tag=hostshare

