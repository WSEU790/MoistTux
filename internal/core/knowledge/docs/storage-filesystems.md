# Storage, filesystems and disk health
> Mounts and fstab, LVM, read-only filesystems, fsck, disk errors and SMART.
Tags: storage, filesystem, fstab, mount, lvm, ext4, xfs, fsck, read-only, smart, disk errors, raid, mdadm

## Inspecting storage

* `lsblk -f` — devices, partitions, filesystem types, UUIDs, mount points.
* `findmnt --real` — what is mounted where, with options. `df -hT` — usage per filesystem.
* LVM: `pvs`, `vgs`, `lvs`. Software RAID: `cat /proc/mdstat`.

In NEXUS: `get_disk_metrics`, catalog `lsblk`, `findmnt`, `fstab`, `df`, `df_inodes`, `swap_show`, `dmesg_errors`, `fstrim`.

## /etc/fstab

Use UUIDs (`UUID=...`) rather than `/dev/sdX` names, which can change. Test changes without rebooting: `mount -a` (mounts everything not yet mounted; errors appear immediately). Add `nofail` (and `x-systemd.device-timeout=10s`) to non-essential mounts so a missing disk does not stop the boot. A broken fstab on a remote server can make it unbootable without console access.

## Filesystem became read-only

Kernel log: `EXT4-fs error ... Remounting filesystem read-only`. The filesystem detected corruption or the disk returned I/O errors.

1. Do not keep writing; read `dmesg -T` for the cause (I/O errors point to the disk).
2. Back up what you can read.
3. A check requires the filesystem unmounted: for the root filesystem, reboot into recovery/rescue mode and run `fsck -f /dev/DEVICE` (ext4) or `xfs_repair` (xfs). Never run fsck on a mounted filesystem.
4. Replace failing disks; on cloud servers, check the provider's status.

## Disk errors and SMART

`dmesg -T | grep -iE 'I/O error|ata[0-9]|nvme|medium error'`. On physical servers install `smartmontools` and run `smartctl -H /dev/sda` (overall health) and `smartctl -a` (reallocated/pending sectors, CRC errors). Rising reallocated or pending sector counts mean replace the disk soon. Cloud virtual disks do not expose SMART.

## LVM tasks

* Extend a logical volume and its filesystem in one step: `lvextend -r -L +20G /dev/VG/LV` (requires free space in the volume group: `vgs`).
* Add a disk to a volume group: `pvcreate /dev/sdb`, `vgextend VG /dev/sdb`.
* Shrinking is risky (ext4 must be unmounted; xfs cannot shrink); take a backup first.

## SSD/thin storage

Periodic TRIM (`fstrim.timer`, enabled by default on Ubuntu) returns unused blocks to SSDs and thin-provisioned cloud disks: `fstrim -av` to run it now.

## Mounting a new disk

```
lsblk                                   # identify the new device, e.g. /dev/sdb
mkfs.ext4 -L data /dev/sdb               # DESTROYS any data on /dev/sdb — double-check the device
mkdir -p /data
echo 'LABEL=data /data ext4 defaults,nofail 0 2' >> /etc/fstab
mount -a && df -h /data
```

Formatting is destructive and irreversible; NEXUS refuses mkfs in shell commands — do it from the console after verifying the device.
