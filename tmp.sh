fio --name=readtest --rw=read --bs=4k --size=1G --numjobs=1 --time_based --runtime=30 --filename=/mnt/mybtrfs/bigfile.img --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/root/rrwtest/var/lib/mysql/ibdata1 --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/mnt/nfs_client/cp-mysql-nfs-raw-4-id-1/var/lib/mysql/ibdata1 --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=1G --numjobs=1 --time_based --runtime=30 --directory=/root/rrwtest --ioengine=libaio --direct=0

mount -t overlay overlay -o lowerdir=/root/overlay-test/low-2:/root/overlay-test/low-1,upperdir=/root/overlay-test/upper,workdir=/root/overlay-test/work /root/overlay-test/merged


fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bs=4k --direct=0 --size=500M --numjobs=4 --runtime=60 --group_reporting --directory=/root/rrwtest

fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bssplit=4k/64:8k/20:16k/8:32k/4:64k/2:128k/1:256k/1 --direct=0 --numjobs=4 --runtime=60 --group_reporting --randrepeat=0 --directory=/root/rrwtest


fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bssplit=4k/64:8k/20:16k/8:32k/4:64k/2:128k/1:256k/1 --direct=1 --numjobs=4 --runtime=60 --group_reporting --randrepeat=0 --directory=/root/rrwtest --readwrite=randread

fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bssplit=4k/64:8k/20:16k/8:32k/4:64k/2:128k/1:256k/1 --direct=1 --numjobs=4 --runtime=60 --group_reporting --randrepeat=0 --directory=/path/to/readonly/directory --filename_format='f.\$filenum' --readwrite=randread

fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bs=4k --direct=1 --numjobs=4 --runtime=60 --group_reporting --directory=/root/rrwtest --filesize=500M --randrepeat=0 --norandommap