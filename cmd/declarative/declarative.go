// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2024 The Falco Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package declarative

import (
	"github.com/spf13/cobra"

	"github.com/falcosecurity/event-generator/cmd/declarative/config"
	"github.com/falcosecurity/event-generator/cmd/declarative/run"
	"github.com/falcosecurity/event-generator/cmd/declarative/test"
)

// New creates a new declarative command.
func New(declarativeEnvKey, envKeysPrefix string) *cobra.Command {
	c := &cobra.Command{
		Use:               "declarative",
		Short:             "Run test(s) specified via a YAML description",
		Long:              "Run test(s) specified via a YAML description",
		DisableAutoGenTag: true,
	}

	commonConf := config.New(c, declarativeEnvKey, envKeysPrefix)

	runCmd := run.New(declarativeEnvKey, envKeysPrefix).Command
	testCmd := test.New(commonConf, false).Command
	c.AddCommand(runCmd)
	c.AddCommand(testCmd)
	return c
}
