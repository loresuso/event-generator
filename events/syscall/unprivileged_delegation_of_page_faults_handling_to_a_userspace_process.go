//go:build linux
// +build linux

// SPDX-License-Identifier: Apache-2.0
/*
Copyright (C) 2024 The Falco Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package syscall

import (
	"golang.org/x/sys/unix"

	"github.com/falcosecurity/event-generator/events"
)

var _ = events.Register(
	UnprivilegedDelegationOfPageFaultsHandlingToAUserspaceProcess,
	events.WithDisabled(), // this rules is not included in falco_rules.yaml (stable rules), so disable the action
)

func UnprivilegedDelegationOfPageFaultsHandlingToAUserspaceProcess(h events.Helper) error {
	// ensure the process is spawned, otherwise we might hit unexpected side effect issues with becameUser()
	if h.Spawned() {
		// to make user.uid != 0
		h.Log().Debug("setuid to something non-root")
		if err := becameUser(h, "daemon"); err != nil {
			return err
		}
		// attempt to create userfaultfd syscall is enough
		_, _, _ = unix.Syscall(unix.SYS_USERFAULTFD, 0, 0, 0)
		return nil
	}
	return h.SpawnAsWithSymlink("child", "syscall.UnprivilegedDelegationOfPageFaultsHandlingToAUserspaceProcess")
}
