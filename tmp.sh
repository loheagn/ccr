fio --name=readtest --rw=read --bs=4k --size=1G --numjobs=1 --time_based --runtime=30 --filename=/mnt/mybtrfs/bigfile.img --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/root/rrwtest/var/lib/mysql/ibdata1 --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/mnt/nfs_client/cp-mysql-nfs-raw-4-id-1/var/lib/mysql/ibdata1 --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/root/ccrrestore/bd17a1a7176de87a81d1dd70ff36e67dc3e7e5eb184a783d68748f581f05c460-912991752/var/lib/mysql/ibdata1 --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/root/testdata --ioengine=libaio --direct=1

fio --name=readtest --rw=read --bs=4k --size=12582912 --numjobs=1 --time_based --runtime=30 --filename=/mnt/mybtrfs/testdata --ioengine=libaio --direct=0

fio --name=readtest --rw=read --bs=4k --size=1G --numjobs=1 --time_based --runtime=30 --directory=/root/rrwtest --ioengine=libaio --direct=0

mount -t overlay overlay -o lowerdir=/root/overlay-test/low-2:/root/overlay-test/low-1,upperdir=/root/overlay-test/upper,workdir=/root/overlay-test/work /root/overlay-test/merged


fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bs=4k --direct=0 --size=500M --numjobs=4 --runtime=60 --group_reporting --directory=/root/rrwtest

fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bssplit=4k/64:8k/20:16k/8:32k/4:64k/2:128k/1:256k/1 --direct=0 --numjobs=4 --runtime=60 --group_reporting --randrepeat=0 --directory=/root/rrwtest


fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bssplit=4k/64:8k/20:16k/8:32k/4:64k/2:128k/1:256k/1 --direct=1 --numjobs=4 --runtime=60 --group_reporting --randrepeat=0 --directory=/root/rrwtest --readwrite=randread

fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bssplit=4k/64:8k/20:16k/8:32k/4:64k/2:128k/1:256k/1 --direct=1 --numjobs=4 --runtime=60 --group_reporting --randrepeat=0 --directory=/path/to/readonly/directory --filename_format='f.\$filenum' --readwrite=randread

fio --name=randread --ioengine=libaio --iodepth=16 --rw=randread --bs=4k --direct=1 --numjobs=4 --runtime=60 --group_reporting --directory=/root/rrwtest --filesize=500M --randrepeat=0 --norandommap

# tc qdisc del dev ens160 root
# tc qdisc add dev ens160 root handle 1: htb default 11
# tc class add dev ens160 parent 1: classid 1:1 htb rate 10kbps
# tc filter add dev ens160 protocol ip parent 1:0 prio 1 u32 match ip dport 2049 0xffff flowid 1:1

# tc qdisc add dev ens160 root handle 1: htb default 1
# tc class add dev ens160 parent 1: classid 1:1 htb rate 10mbit
# tc class add dev ens160 parent 1: classid 1:10 htb rate 1mbit
# tc filter add dev ens160 protocol ip parent 1:0 prio 1 u32 match ip dst 10.251.255.97 flowid 1:10

iptables -N MYCHAIN

# 标记来自特定 IP 的数据包，将源 IP 替换为你想要限制的 IP
iptables -A MYCHAIN -s 10.251.255.97 -j MARK --set-mark 1

# 将标记链应用到 FORWARD 链（如果限制的是进入服务器的流量）
iptables -A FORWARD -j MYCHAIN

tc qdisc add dev ens160 root handle 1: htb default 30

# 创建一个类，这里限制带宽为 1mbit
tc class add dev ens160 parent 1: classid 1:1 htb rate 1mbit

# 应用一个过滤器，将标记为 1 的数据包定向到这个类
tc filter add dev ens160 parent 1: protocol ip prio 1 handle 1 fw classid 1:1

fio --name=readtest --rw=read --bs=4k --size=1G --numjobs=1 --time_based --runtime=30 --filename=/root/ccrrestore/71f43c99065ff342601e7b2834caac2b604e4083f23792008e59e3274235292c-4026208587/root/bigfile.img --ioengine=libaio --direct=1

fio --name=readwritetest --eta-newline=5s --filename=testfile --rw=randwrite --size=500M --ioengine=libaio --direct=0 --numjobs=4 --runtime=60 --group_reporting --filename=/run/containerd/io.containerd.runtime.v2.task/k8s.io/a360323e7e56660d0d40c4a5e97cd5083891f7866cb907aff5ca68a44c9ce555/rootfs/root/bigfile.img


fio --name=readtest --rw=read --bs=4k --size=1G --numjobs=1 --time_based --runtime=30 --filename=/mnt/nfs_client/bigfile.img --ioengine=libaio --direct=1

fio --name=readwritetest --eta-newline=5s --filename=testfile --rw=randwrite --size=500M --ioengine=libaio --direct=1 --numjobs=4 --runtime=60 --group_reporting --filename=/root/overlay-test/merged/bigfile.img

