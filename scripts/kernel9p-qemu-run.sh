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

KERNEL9P_SERVER="${KERNEL9P_SERVER:-qemu}" # qemu | diod
KERNEL9P_TCP_PORT="${KERNEL9P_TCP_PORT:-564}"

mkdir -p "${OUT_DIR}" "${SHARE_DIR}"

case "${KERNEL_ARCH}" in
  amd64)
    QEMU_BIN="${QEMU_BIN:-qemu-system-x86_64}"
    KERNEL_PATH="${KERNEL_BZIMAGE}"
    CONSOLE="ttyS0"
    VIRTIO_9P_DEVICE=(-device virtio-9p-pci,fsdev=fsdev0,mount_tag=hostshare)
    NETDEV_ARGS=(-netdev user,id=net0 -device virtio-net-pci,netdev=net0)
    MACHINE_ARGS_BASE=(-machine q35 -cpu max -device virtio-rng-pci)
    ;;
  arm64)
    QEMU_BIN="${QEMU_BIN:-qemu-system-aarch64}"
    KERNEL_PATH="${KERNEL_IMAGE}"
    CONSOLE="ttyAMA0"
    VIRTIO_9P_DEVICE=(-device virtio-9p-device,fsdev=fsdev0,mount_tag=hostshare)
    NETDEV_ARGS=(-netdev user,id=net0 -device virtio-net-device,netdev=net0)
    MACHINE_ARGS_BASE=(-machine virt -cpu cortex-a57 -device virtio-rng-device)
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

"${QEMU_BIN}" --version >/dev/null 2>&1 || {
  echo "Missing QEMU binary at ${QEMU_BIN}" >&2
  exit 2
}

cleanup() {
  if [[ -n "${DIOD_PID:-}" ]]; then
    kill "${DIOD_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

QEMU_ARGS=(
  -nodefaults
  -no-reboot
  -m 1024
  -smp 1
  -accel tcg
  -serial mon:stdio
  -nographic
  -kernel "${KERNEL_PATH}"
  -initrd "${INITRAMFS_GZ}"
  -append "console=${CONSOLE} panic=1 oops=panic loglevel=7"
  "${MACHINE_ARGS_BASE[@]}"
)

case "${KERNEL9P_SERVER}" in
  qemu)
    QEMU_ARGS+=(
      -fsdev local,id=fsdev0,path="${SHARE_DIR}",security_model=none
      "${VIRTIO_9P_DEVICE[@]}"
    )
    ;;
  diod)
    if ! command -v diod >/dev/null 2>&1; then
      echo "Missing diod; install it or use KERNEL9P_SERVER=qemu" >&2
      exit 2
    fi
    echo "Starting diod 9p server on 0.0.0.0:${KERNEL9P_TCP_PORT} exporting ${SHARE_DIR}..."
    diod --listen="0.0.0.0:${KERNEL9P_TCP_PORT}" --no-auth --export="${SHARE_DIR}" >/dev/null 2>&1 &
    DIOD_PID="$!"
    QEMU_ARGS+=("${NETDEV_ARGS[@]}")
    ;;
  *)
    echo "Unsupported KERNEL9P_SERVER=${KERNEL9P_SERVER} (expected qemu or diod)" >&2
    exit 2
    ;;
esac

"${QEMU_BIN}" "${QEMU_ARGS[@]}"

