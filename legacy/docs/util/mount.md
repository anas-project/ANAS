
## 格式化硬盘

### 分区

```
# lsblk //查看存储设备
NAME            MAJ:MIN RM  SIZE RO TYPE MOUNTPOINT
sdb               8:16   0  1.8T  0 disk
//新插入的硬盘
```
使用`gdisk`进行分区，分区表格式是`gpt`。

```
# gdisk /dev/[设备名sdb] //输入相应的存储设备
GPT fdisk (gdisk) version 0.8.10

Partition table scan:
  MBR: not present
  BSD: not present
  APM: not present
  GPT: not present

Creating new GPT entries.

Command (? for help):
```
`n`新建分区表；分区数字，输入1或回车默认；第一个扇区，回车默认；最后一个扇区，回车默认；输入`w`写入硬盘。这样我们就建立一个占满整个硬盘的一个分区。

```
Command (? for help): n
Partition number (1-128, default 1): 1
First sector (34-3907029134, default = 2048) or {+-}size{KMGTP}:
Last sector (2048-3907029134, default = 3907029134) or {+-}size{KMGTP}:
Current type is 'Linux filesystem'
Hex code or GUID (L to show codes, Enter = 8300):
Changed type of partition to 'Linux filesystem'

Command (? for help): w

Final checks complete. About to write GPT data. THIS WILL OVERWRITE EXISTING
PARTITIONS!!

Do you want to proceed? (Y/N): y
OK; writing new GUID partition table (GPT) to /dev/sdb.
The operation has completed successfully.
```

查看存储设备

```
# lsblk
NAME            MAJ:MIN RM  SIZE RO TYPE MOUNTPOINT
sdb               8:16   0  1.8T  0 disk
└─sdb1            8:17   0  1.8T  0 part
```

格式化新创建的分区

```
# mkfs.ext4 /dev/[新创建的分区名sdb1]
```

新建挂载文件夹，如`mkdir /data`；查看分区的`UUID`，复制其中的`UUID`；编辑`/etc/fstab`；在文件最下方插入`UUID=[上文复制的UUID] /data	ext4	defaults	0 0`；使用`mount -a`更新挂载项；使用`df`查看挂在情况。

```
# blkid /dev/[新创建的分区名sdb1]
# vim /etc/fstab
# mount -a
# df
/dev/sdb1               1922720004   77848 1824950096   1% /data //挂载成功
```


