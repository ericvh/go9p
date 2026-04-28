#!/usr/bin/env bash
set -euo pipefail

echo "[guest] running inside chroot: $(uname -rm)"

SERVER_ADDR="${KERNEL9P_TCP_ADDR:-10.0.2.2}"
SERVER_PORT="${KERNEL9P_TCP_PORT:-564}"
FS="${KERNEL9P_FS:-ufs}"
UNAME="${KERNEL9P_UNAME:-root}"
UID_OPT="${KERNEL9P_UID:-0}"
GID_OPT="${KERNEL9P_GID:-0}"

# The virtio-9p "hostshare" mount is the container root; we may not be able to
# create new top-level directories there. /tmp should be writable.
MNT_BASE="${KERNEL9P_MOUNT:-/tmp/kernel9p-mnt}"

mount_and_run() {
  local version="$1"
  local mnt="$2"
  local server_mode="$3"

  mkdir -p "${mnt}"
  echo "[guest] mounting kernel 9p client via tcp ${SERVER_ADDR}:${SERVER_PORT} (fs=${FS} version=${version}) -> ${mnt}"
  opts="trans=tcp,version=${version},msize=262144,port=${SERVER_PORT},uname=${UNAME},uid=${UID_OPT},gid=${GID_OPT}"
  # Some synthetic servers don't model full Unix permission semantics; permit access for CLI checks.
  if [ "${FS}" = "deviceconnect" ]; then
    opts="${opts},access=any"
  fi
  mount -t 9p -o "${opts}" "${SERVER_ADDR}" "${mnt}"

  echo "[guest] running kernel9p-e2e against mount ${mnt} (fs=${FS} server=${server_mode})"
  KERNEL9P_FS="${FS}" KERNEL9P_SERVER="${server_mode}" KERNEL9P_TCP_ADDR="${SERVER_ADDR}" KERNEL9P_TCP_PORT="${SERVER_PORT}" KERNEL9P_MOUNT="${mnt}" /opt/v9fs/kernel9p-e2e

  # Also exercise the "kernel-mounted" CLI tools against the mounted tree.
  case "${FS}" in
    netfs)
      echo "[guest] running kernel-mounted CLI checks (netfs)"
      /opt/v9fs/go9p-knetfs -netroot "${mnt}/net" tcp-alloc >/dev/null
      ;;
    deviceconnect)
      echo "[guest] running kernel-mounted CLI checks (deviceconnect)"
      /opt/v9fs/go9p-kdeviceconnect -root "${mnt}/devices" discover >/dev/null
      /opt/v9fs/go9p-kdeviceconnect -root "${mnt}/devices" meta -id robot-001 >/dev/null
      /opt/v9fs/go9p-kdeviceconnect -root "${mnt}/devices" status -id robot-001 >/dev/null
      ;;
  esac

  echo "[guest] unmounting ${mnt}"
  umount "${mnt}"
}

case "${FS}" in
  ufs)
    # For ufs we also run the go9p client↔server CRUD check (server_mode=go9p-ufs).
    mount_and_run "9p2000.u" "${MNT_BASE}-9p2000u" "go9p-ufs"
    mount_and_run "9p2000" "${MNT_BASE}-9p2000" "kernel-only"
    ;;
  ramfs|clonefs|timefs|netfs)
    # Kernel mount smoke only; behavior is tailored inside kernel9p-e2e by KERNEL9P_FS.
    mount_and_run "9p2000.u" "${MNT_BASE}-9p2000u" "kernel-only"
    mount_and_run "9p2000" "${MNT_BASE}-9p2000" "kernel-only"
    ;;
  deviceconnect)
    # Kernel mount + CLI exercise only.
    mount_and_run "9p2000.u" "${MNT_BASE}-9p2000u" "kernel-only"
    mount_and_run "9p2000" "${MNT_BASE}-9p2000" "kernel-only"
    ;;
  *)
    echo "[guest] unknown KERNEL9P_FS=${FS}" >&2
    exit 2
    ;;
esac

echo "[guest] PASS"
exit 0

