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
    bool is_write;
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

int probe_read(struct pt_regs *ctx, struct file *file, loff_t *pos) {
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
    task_info->is_write = false;
    bpf_get_current_comm(&task_info->comm, 80);

    bpf_ringbuf_submit(task_info, 0);

    bpf_map_delete_elem(&entries, &id);

    return 0;
}

int probe_write(struct pt_regs *ctx) {
    struct event *task_info;
    task_info = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
    if (!task_info) {
        return 0;
    }

    u64 id = bpf_get_current_pid_tgid();
    task_info->pid = id >> 32;
    struct file *file = (struct file *)PT_REGS_PARM1(ctx);
    task_info->inode = BPF_CORE_READ(file, f_inode, i_ino);
    task_info->is_write = true;
    bpf_get_current_comm(&task_info->comm, 80);

    bpf_ringbuf_submit(task_info, 0);
    return 0;
}

SEC("kprobe/kernel_read")
int BPF_KPROBE(kernel_read, struct file *file, const char *buf, size_t count,
               loff_t *pos) {
    return probe_read(ctx, file, pos);
}

SEC("kprobe/vfs_read")
int BPF_KPROBE(vfs_read, struct file *file, const char *buf, size_t count,
               loff_t *pos) {
    return probe_read(ctx, file, pos);
}

SEC("kprobe/vfs_readv")
int vfs_readv(struct pt_regs *ctx) {
    struct file *file = (struct file *)PT_REGS_PARM1(ctx);
    loff_t *pos = (loff_t *)PT_REGS_PARM3(ctx);
    return probe_read(ctx, file, pos);
}

SEC("kprobe/vfs_iter_read")
int vfs_iter_read(struct pt_regs *ctx) {
    struct file *file = (struct file *)PT_REGS_PARM1(ctx);
    loff_t *pos = (loff_t *)PT_REGS_PARM3(ctx);
    return probe_read(ctx, file, pos);
}

SEC("kretprobe/kernel_read")
int BPF_KRETPROBE(kernel_read_ret) { return probe_ret(ctx); }

SEC("kretprobe/vfs_read")
int BPF_KRETPROBE(vfs_read_ret) { return probe_ret(ctx); }

SEC("kretprobe/vfs_readv")
int BPF_KRETPROBE(vfs_readv_ret) { return probe_ret(ctx); }

SEC("kretprobe/vfs_iter_read")
int BPF_KRETPROBE(vfs_iter_read_ret) { return probe_ret(ctx); }

SEC("kprobe/kernel_write")
int kernel_write(struct pt_regs *ctx) { return probe_write(ctx); }

SEC("kprobe/vfs_write")
int vfs_write(struct pt_regs *ctx) { return probe_write(ctx); }

SEC("kprobe/vfs_writev")
int vfs_writev(struct pt_regs *ctx) { return probe_write(ctx); }

SEC("kprobe/vfs_iter_write")
int vfs_iter_write(struct pt_regs *ctx) { return probe_write(ctx); }