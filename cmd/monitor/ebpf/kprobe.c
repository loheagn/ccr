// +build ignore
#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
char __license[] SEC("license") = "Dual MIT/GPL";

struct event {
    u32 pid;
    u64 inode;
    loff_t pos;
    size_t ret;
    u8 comm[80];
};

static struct event zero_value = {};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 24);
} events SEC(".maps");

#define MAX_ENTRIES 10240
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_ENTRIES);
    __type(key, u64);
    __type(value, struct event);
} entries SEC(".maps");

// static void get_file_path(struct file *file, char *buf, size_t size) {
//     struct qstr dname;

//     dname = BPF_CORE_READ(file, f_path.dentry, d_name);
//     bpf_probe_read_kernel(buf, 80, dname.name);
// }

// Force emitting struct event into the ELF.
const struct event *unused __attribute__((unused));

int probe_read(struct pt_regs *ctx, struct file *file, const char *buf,
               size_t count, loff_t *pos) {
    u64 id = bpf_get_current_pid_tgid();
    struct event *task_info;
    task_info = bpf_map_lookup_elem(&entries, &id);
    if (!task_info) {
        bpf_map_update_elem(&entries, &id, &zero_value, BPF_ANY);
        task_info = bpf_map_lookup_elem(&entries, &id);
    }

    if (task_info) {
        u32 tgid = id >> 32;
        task_info->pid = tgid;
        task_info->inode = BPF_CORE_READ(file, f_inode, i_ino);
        bpf_probe_read_kernel(&(task_info->pos), sizeof(loff_t), pos);
    }

    return 0;
}

int probe_ret(struct pt_regs *ctx) {
    u64 id = bpf_get_current_pid_tgid();
    struct event *old_task_info;
    old_task_info = bpf_map_lookup_elem(&entries, &id);
    if (!old_task_info) {
        return 0;
    }

    struct event *task_info;
    task_info = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
    if (!task_info) {
        return 0;
    }

    task_info->ret = PT_REGS_RC(ctx);
    task_info->pid = id >> 32;
    task_info->inode = old_task_info->inode;
    task_info->pos = old_task_info->pos;
    bpf_get_current_comm(&task_info->comm, 80);

    bpf_ringbuf_submit(task_info, 0);

    bpf_map_delete_elem(&entries, &id);

    return 0;
}

SEC("kprobe/vfs_read")
int BPF_KPROBE(vfs_read, struct file *file, const char *buf, size_t count,
               loff_t *pos) {
    return probe_read(ctx, file, buf, count, pos);
}

SEC("kprobe/kernel_read")
int BPF_KPROBE(kernel_read, struct file *file, const char *buf, size_t count,
               loff_t *pos) {
    return probe_read(ctx, file, buf, count, pos);
}

SEC("kretprobe/vfs_read")
int BPF_KRETPROBE(vfs_ret_read) { return probe_ret(ctx); }

SEC("kretprobe/kernel_read")
int BPF_KRETPROBE(kernel_ret_read) { return probe_ret(ctx); }
