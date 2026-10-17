// Package memoryguard provides the process-local container memory budget used
// by Geili's runtime and request admission guards.
package memoryguard

import (
	"math"
	"os"
	"path"
	"strconv"
	"strings"
)

// ContainerMemoryLimit returns the smallest finite hard memory limit visible
// for the current cgroup and its mounted ancestors. Missing, unlimited, or
// malformed cgroup data leaves non-container deployments unchanged.
func ContainerMemoryLimit() (int64, bool) {
	return containerMemoryLimitGeili(os.ReadFile)
}

type memoryMountGeili struct {
	root, point string
	v2          bool
}

func containerMemoryLimitGeili(readFile func(string) ([]byte, error)) (int64, bool) {
	groups, _ := readFile("/proc/self/cgroup")
	mounts, _ := readFile("/proc/self/mountinfo")
	v2Group, v1Group := memoryGroupPathsGeili(string(groups))
	var smallest int64
	readLimit := func(filename string, v2 bool) {
		raw, err := readFile(filename)
		if err != nil {
			return
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || value == 0 || value >= math.MaxInt64 {
			return
		}
		// cgroup v1 represents unlimited with a page-rounded LONG_MAX value.
		if !v2 && value >= 1<<60 {
			return
		}
		if smallest == 0 || int64(value) < smallest {
			smallest = int64(value)
		}
	}
	for _, mount := range memoryMountsGeili(string(mounts)) {
		group := v1Group
		filename := "memory.limit_in_bytes"
		if mount.v2 {
			group = v2Group
			filename = "memory.max"
		}
		if group == "" {
			continue
		}
		relative := strings.TrimPrefix(group, "/")
		if group == mount.root {
			relative = ""
		} else if strings.HasPrefix(group, mount.root+"/") {
			relative = strings.TrimPrefix(group, mount.root+"/")
		}
		// With a cgroup namespace, /proc/self/cgroup is relative to that
		// namespace even when mountinfo exposes a non-root physical root.
		current := path.Join(mount.point, relative)
		for {
			readLimit(path.Join(current, filename), mount.v2)
			if current == mount.point {
				break
			}
			parent := path.Dir(current)
			if parent == current || (mount.point != "/" && parent != mount.point && !strings.HasPrefix(parent, mount.point+"/")) {
				break
			}
			current = parent
		}
	}
	// Standard mount paths also support minimal containers without readable
	// proc metadata. They do not infer a budget from host RAM.
	readLimit("/sys/fs/cgroup/memory.max", true)
	readLimit("/sys/fs/cgroup/memory/memory.limit_in_bytes", false)
	return smallest, smallest > 0
}

func memoryGroupPathsGeili(raw string) (v2, v1 string) {
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || !validCgroupPathGeili(parts[2]) {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			v2 = parts[2]
		} else if hasControllerGeili(parts[1], "memory") {
			v1 = parts[2]
		}
	}
	return v2, v1
}

func memoryMountsGeili(raw string) []memoryMountGeili {
	var mounts []memoryMountGeili
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 {
			continue
		}
		fields, fs := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(fields) < 6 || len(fs) < 3 {
			continue
		}
		v2 := fs[0] == "cgroup2"
		if !v2 && (fs[0] != "cgroup" || !hasControllerGeili(fs[2], "memory")) {
			continue
		}
		root, point := unescapeMountPathGeili(fields[3]), unescapeMountPathGeili(fields[4])
		if validCgroupPathGeili(root) && validCgroupPathGeili(point) {
			mounts = append(mounts, memoryMountGeili{root: root, point: point, v2: v2})
		}
	}
	return mounts
}

func validCgroupPathGeili(value string) bool {
	if !strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return !strings.ContainsRune(value, '\x00')
}

func hasControllerGeili(raw, controller string) bool {
	for _, item := range strings.Split(raw, ",") {
		if item == controller {
			return true
		}
	}
	return false
}

func unescapeMountPathGeili(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}
