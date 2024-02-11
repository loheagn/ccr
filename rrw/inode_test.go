package rrw

import (
	"encoding/json"
	"reflect"
	"testing"
)

func Test_wrapInode(t *testing.T) {
	type args struct {
		inode     *RRWInode
		inodeType InodeType
	}
	tests := []struct {
		name string
		args args
		want *InodeWrapper
	}{
		{
			name: "test1",
			args: args{
				inode: &RRWInode{
					Size: 6,
				},
				inodeType: InodeTypeRRW,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapInode(tt.args.inode, tt.args.inodeType)
			data, _ := json.Marshal(got)
			gott := InodeWrapper{}
			json.Unmarshal(data, &gott)
			if !reflect.DeepEqual(got, &gott) {
				t.Errorf("wrapInode() = %v, want %v", got, &gott)
			}
		})
	}
}
