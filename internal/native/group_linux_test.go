package native

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestGroupScanProcessDisappearance(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"100", "101", "102", "103", "not-a-process"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []error{syscall.ENOENT, syscall.ESRCH} {
		t.Run(gone.Error(), func(t *testing.T) {
			members, err := scanGroupMembers(42, entries, func(pid int) ([]string, error) {
				if pid == 101 {
					return nil, &os.PathError{Op: "read", Path: "/proc/101/stat", Err: gone}
				}
				group := "42"
				if pid == 102 {
					group = "99"
				}
				return []string{"S", "1", group}, nil
			})
			if err != nil || !reflect.DeepEqual(members, []int{100, 103}) {
				t.Fatal("vanished process prevented observing retained group", members, err)
			}
		})
	}
	for _, uncertain := range []error{syscall.EACCES, syscall.EIO} {
		_, err := scanGroupMembers(42, entries, func(int) ([]string, error) {
			return nil, fmt.Errorf("process observation: %w", uncertain)
		})
		if !errors.Is(err, uncertain) {
			t.Fatal("uncertain observation accepted", uncertain, err)
		}
	}
}
