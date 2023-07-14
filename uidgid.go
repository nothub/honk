package main

/*
#include <stdlib.h>
#include <unistd.h>
*/
import "C"

import (
	"syscall"
)

func setUidGid(uid int, gid int) {
	dlog.Printf("getuid() returned %s", syscall.Getuid())
	dlog.Printf("getgid() returned %s", syscall.Getgid())

	if err := syscall.Setgroups([]int{}); err != nil {
		elog.Fatalf("setgroups() failure (%d)", err)
	}

	if err := syscall.Setgid(gid); err != nil {
		elog.Fatalf("Setgid(%s) failure (%d)", gid, err)
	}

	if err := syscall.Setuid(uid); err != nil {
		elog.Fatalf("Setuid(%s) failure (%d)", uid, err)
	}

	dlog.Printf("getuid() returned %s", syscall.Getuid())
	dlog.Printf("getgid() returned %s", syscall.Getgid())
}

func init() {
	preservehooks = append(preservehooks, func() {
		// TODO: uid and gid flag
		//setUidGid(10042, 10042)
	})
}
