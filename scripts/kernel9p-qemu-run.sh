#!/usr/bin/env bash
set -euo pipefail

# Runs a Linux kernel under QEMU and validates the kernel 9p client by
# mounting a virtio-9p export (QEMU's built-in server) and executing a
# small test binary inside the guest.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/.kernel9p-out}"

KERNEL_ARCH="${KERNEL_ARCH:-amd64}"
KERNEL_BZIMAGE="${KERNEL_BZIMAGE:-${OUT_DIR}/linux/arch/x86/boot/bzImage}"
KERNEL_IMAGE="${KERNEL_IMAGE:-${OUT_DIR}/linux/arch/arm64/boot/Image}"
INITRAMFS_GZ="${INITRAMFS_GZ:-${OUT_DIR}/initramfs.cpio.gz}"
SHARE_DIR="${SHARE_DIR:-${OUT_DIR}/share}"

mkdir -p "${OUT_DIR}" "${SHARE_DIR}"

case "${KERNEL_ARCH}" in
  amd64)
    QEMU_BIN="${QEMU_BIN:-qemu-system-x86_64}"
    KERNEL_PATH="${KERNEL_BZIMAGE}"
    CONSOLE="ttyS0"
    MACHINE_ARGS=(-machine q35 -cpu max -device virtio-rng-pci -device virtio-9p-pci,fsdev=fsdev0,mount_tag=hostshare)
    ;;
  arm64)
    QEMU_BIN="${QEMU_BIN:-qemu-system-aarch64}"
    KERNEL_PATH="${KERNEL_IMAGE}"
    CONSOLE="ttyAMA0"
    MACHINE_ARGS=(-machine virt -cpu cortex-a57 -device virtio-rng-device -device virtio-9p-device,fsdev=fsdev0,mount_tag=hostshare)
    ;;
  *)
    echo "Unsupported KERNEL_ARCH=${KERNEL_ARCH} (expected amd64 or arm64)" >&2
    exit 2
    ;;
esac

if [[ ! -f "${KERNEL_PATH}" ]]; then
  echo "Missing kernel image at ${KERNEL_PATH}" >&2
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
  -smp 1 \
  -accel tcg \
  -serial mon:stdio \
  -nographic \
  -kernel "${KERNEL_PATH}" \
  -initrd "${INITRAMFS_GZ}" \
  -append "console=${CONSOLE} panic=1 oops=panic loglevel=7" \
  -fsdev local,id=fsdev0,path="${SHARE_DIR}",security_model=none \
  "${MACHINE_ARGS[@]}"

