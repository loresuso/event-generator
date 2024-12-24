// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2025 The Falco Authors
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

package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/thediveo/enumflag"

	"github.com/falcosecurity/event-generator/cmd/declarative/config"
	"github.com/falcosecurity/event-generator/pkg/alert/retriever/grpcretriever"
	containerbuilder "github.com/falcosecurity/event-generator/pkg/container/builder"
	"github.com/falcosecurity/event-generator/pkg/label"
	processbuilder "github.com/falcosecurity/event-generator/pkg/process/builder"
	"github.com/falcosecurity/event-generator/pkg/test"
	testbuilder "github.com/falcosecurity/event-generator/pkg/test/builder"
	"github.com/falcosecurity/event-generator/pkg/test/loader"
	resbuilder "github.com/falcosecurity/event-generator/pkg/test/resource/builder"
	"github.com/falcosecurity/event-generator/pkg/test/runner"
	runnerbuilder "github.com/falcosecurity/event-generator/pkg/test/runner/builder"
	stepbuilder "github.com/falcosecurity/event-generator/pkg/test/step/builder"
	sysbuilder "github.com/falcosecurity/event-generator/pkg/test/step/syscall/builder"
	"github.com/falcosecurity/event-generator/pkg/test/tester"
	"github.com/falcosecurity/event-generator/pkg/test/tester/reportencoder/jsonencoder"
	"github.com/falcosecurity/event-generator/pkg/test/tester/reportencoder/textencoder"
	"github.com/falcosecurity/event-generator/pkg/test/tester/reportencoder/yamlencoder"
	testerimpl "github.com/falcosecurity/event-generator/pkg/test/tester/tester"
)

const (
	// testIDIgnorePrefix is the prefix used to mark a process as not monitored.
	testIDIgnorePrefix = "ignore:"
)

const (
	longDescriptionPrefaceTemplate = `%s.
It is possible to provide the YAML description in multiple ways. The order of evaluation is the following:
1) If the --%s=<file_path> flag is provided the description is read from the file at <file_path>
2) If the --%s=<description> flag is provided, the description is read from the <description> string
3) Otherwise, it is read from standard input`
	longDescriptionHeading = "Run test(s) specified via a YAML description and verify that they produce the expected outcomes"
	warningMessage         = `ReportWarning:
  This command might alter your system. For example, some actions modify files and directories below /bin, /etc, /dev,
  etc... Make sure you fully understand what is the purpose of this tool before running any action.`
)

var (
	longDescriptionPreface = fmt.Sprintf(longDescriptionPrefaceTemplate, longDescriptionHeading,
		config.DescriptionFileFlagName, config.DescriptionFlagName)
	longDescription = fmt.Sprintf("%s\n\n%s", longDescriptionPreface, warningMessage)
)

// reportFormat defines the types of format used for outputting a tester report.
type reportFormat int

const (
	// reportFormatText specifies to format a tester report using a formatted text encoding.
	reportFormatText reportFormat = iota
	// reportFormatJSON specifies to format a tester report using a JSON encoding.
	reportFormatJSON
	// reportFormatYAML specifies to format a tester report using a YAML text encoding.
	reportFormatYAML
)

var reportFormats = map[reportFormat][]string{
	reportFormatText: {"text"},
	reportFormatJSON: {"json"},
	reportFormatYAML: {"yaml"},
}

// CommandWrapper is a wrapper around the test command storing the flag values bound to the command at runtime.
type CommandWrapper struct {
	*config.Config
	Command                 *cobra.Command
	skipOutcomeVerification bool
	unixSocketPath          string
	hostname                string
	port                    uint16
	certFile                string
	keyFile                 string
	caRootFile              string
	pollingTimeout          time.Duration
	reportFormat            reportFormat
}

// New creates a new test command.
func New(commonConf *config.Config, skipOutcomesVerification bool) *CommandWrapper {
	cw := &CommandWrapper{Config: commonConf, skipOutcomeVerification: skipOutcomesVerification}

	c := &cobra.Command{
		Use:               "test",
		Short:             longDescriptionHeading,
		Long:              longDescription,
		DisableAutoGenTag: true,
		Run:               cw.run,
	}

	cw.initFlags(c)

	cw.Command = c
	return cw
}

// initFlags initializes the provided command's flags.
func (cw *CommandWrapper) initFlags(c *cobra.Command) {
	// Initialize command's flags with the ones exported by the config.
	cw.Config.InitCommandFlags(c)

	// The following flags are all associated with outcome verification, so early return if we are going to skip that
	// step.
	if cw.skipOutcomeVerification {
		return
	}

	flags := c.Flags()

	flags.BoolVar(&cw.skipOutcomeVerification, "skip-outcome-verification", false,
		"Skip verification of the expected outcome. If this option is enabled, grpc- flags are ignored")
	flags.StringVar(&cw.unixSocketPath, "grpc-unix-socket", "",
		"The unix socket path of the local Falco instance (use only if you want to connect to Falco through a "+
			"unix socket)")
	flags.StringVar(&cw.hostname, "grpc-hostname", "localhost",
		"The Falco gRPC server hostname")
	flags.Uint16Var(&cw.port, "grpc-port", 5060, "The Falco gRPC server port")
	flags.StringVar(&cw.certFile, "grpc-cert", "/etc/falco/certs/client.crt",
		"The path of the client certificate to be used for mutual TLS against the Falco gRPC server")
	flags.StringVar(&cw.keyFile, "grpc-key", "/etc/falco/certs/client.key",
		"The path of the client private key to be used for mutual TLS against the Falco gRPC server")
	flags.StringVar(&cw.caRootFile, "grpc-ca", "/etc/falco/certs/ca.crt",
		"The path of the CA root certificate used for Falco gRPC server's certificate validation")
	flags.DurationVar(&cw.pollingTimeout, "grpc-poll-timeout", 100*time.Millisecond,
		"The frequency of the watch operation on the gRPC Falco Outputs API stream")
	flags.Var(
		enumflag.New(&cw.reportFormat, "report-format", reportFormats, enumflag.EnumCaseInsensitive),
		"report-format", "The format of the test report; can be 'text', 'json' or 'yaml'")
}

// envKeyFromFlagName converts the provided flag name into the corresponding environment variable key.
func envKeyFromFlagName(envKeysPrefix, flagName string) string {
	s := fmt.Sprintf("%s_%s", envKeysPrefix, strings.ToUpper(flagName))
	s = strings.ToUpper(s)
	return strings.ReplaceAll(s, "-", "_")
}

// run runs the provided command with the provided arguments.
func (cw *CommandWrapper) run(cmd *cobra.Command, _ []string) {
	ctx := cmd.Context()
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(fmt.Sprintf("logger unconfigured: %v", err))
	}

	ctx, cancel := context.WithTimeout(ctx, cw.TestsTimeout)
	defer cancel()
	exitAndCancel := func() {
		cancel()
		os.Exit(1)
	}

	labels, err := label.ParseSet(cw.Labels)
	if err != nil {
		logger.Error(err, "Error parsing labels")
		exitAndCancel()
	}

	logger = enrichLoggerWithLabels(logger, labels)

	description, err := loadTestsDescription(logger, cw.TestsDescriptionFile, cw.TestsDescription)
	if err != nil {
		logger.Error(err, "Error loading tests description")
		exitAndCancel()
	}

	runnerBuilder, err := cw.createRunnerBuilder()
	if err != nil {
		logger.Error(err, "Error creating runner builder")
		exitAndCancel()
	}

	// Retrieve the already populated test ID. The test ID absence is used to uniquely identify the root process in the
	// process chain.
	testID := cw.TestID
	isRootProcess := testID == ""

	// Initialize tester and Falco alerts collection.
	var testr tester.Tester
	testerWaitGroup := sync.WaitGroup{}
	if isRootProcess && !cw.skipOutcomeVerification {
		if testr, err = cw.createTester(logger); err != nil {
			logger.Error(err, "Error creating tester")
			exitAndCancel()
		}
		go func() {
			if err := testr.StartAlertsCollection(ctx); err != nil {
				logger.Error(err, "Error starting tester execution")
				exitAndCancel()
			}
		}()
	}

	// Prepare parameters shared by all runners.
	runnerEnviron := cw.buildRunnerEnviron(cmd)
	var runnerLabels *label.Set
	if labels != nil {
		runnerLabels = labels.Clone()
	}

	// Build and run the tests.
	for testIndex := range description.Tests {
		testDesc := &description.Tests[testIndex]

		// If this process belongs to a test process chain, override the logged test index in order to match its
		// absolute index among all the test descriptions.
		if labels != nil {
			testIndex = labels.TestIndex
		}

		logger := logger.WithValues("testName", testDesc.Name, "testIndex", testIndex)

		var testUID uuid.UUID
		if isRootProcess {
			// Generate a new UID for the test.
			testUID = uuid.New()
			testID = newTestID(&testUID)
			ensureProcessChainLeaf(testDesc)
		} else {
			// Extract UID from test ID.
			var err error
			testUID, err = extractTestUID(testID)
			if err != nil {
				logger.Error(err, "Error extracting test UID from test ID", "testId", testID)
				exitAndCancel()
			}
		}

		logger = logger.WithValues("testUid", testUID)

		runnerLogger := logger.WithName("runner")
		runnerDescription := &runner.Description{
			Environ:                   runnerEnviron,
			TestDescriptionEnvKey:     cw.DescriptionEnvKey,
			TestDescriptionFileEnvKey: cw.DescriptionFileEnvKey,
			TestIDEnvKey:              cw.TestIDEnvKey,
			TestIDIgnorePrefix:        testIDIgnorePrefix,
			LabelsEnvKey:              cw.LabelsEnvKey,
			Labels:                    runnerLabels,
		}
		runnerInstance, err := runnerBuilder.Build(testDesc.Runner, runnerLogger, runnerDescription)
		if err != nil {
			logger.Error(err, "Error creating runner")
			exitAndCancel()
		}

		logger.Info("Starting test execution...")
		if err := runnerInstance.Run(ctx, testID, testIndex, testDesc); err != nil {
			logRunnerError(logger, err)
			exitAndCancel()
		}

		logger.Info("Test execution completed")

		if testr != nil {
			produceReport(&testerWaitGroup, testr, &testUID, testDesc, cw.reportFormat)
		}
	}

	testerWaitGroup.Wait()
}

// enrichLoggerWithLabels creates a new logger, starting from the provided one, with the information extracted from the
// provided labels.
func enrichLoggerWithLabels(logger logr.Logger, labels *label.Set) logr.Logger {
	if labels == nil {
		return logger.WithName("root")
	}

	testName := fmt.Sprintf("test%d", labels.TestIndex)
	logger = logger.WithName(testName)
	if labels.IsContainer {
		logger = logger.WithName("cont")
		if imageName := labels.ImageName; imageName != "" {
			logger = logger.WithValues("imageName", imageName)
		}
		if containerName := labels.ContainerName; containerName != "" {
			logger = logger.WithValues("containerName", containerName)
		}
	}
	procName := fmt.Sprintf("proc%d", labels.ProcIndex)
	return logger.WithName(procName)
}

// loadTestsDescription loads the YAML tests description from a different source, depending on the content of the
// provided values:
// - if the provided descriptionFilePath is not empty, the description is loaded from the specified file
// - if the provided description is not empty, the description is loaded from its content
// - otherwise, it is loaded from standard input.
func loadTestsDescription(logger logr.Logger, descriptionFilePath, description string) (*loader.Description, error) {
	ldr := loader.New()

	if descriptionFilePath != "" {
		descriptionFilePath = filepath.Clean(descriptionFilePath)
		descriptionFile, err := os.Open(descriptionFilePath)
		if err != nil {
			return nil, fmt.Errorf("error opening description file %q: %w", descriptionFilePath, err)
		}
		defer func() {
			if err := descriptionFile.Close(); err != nil {
				logger.Error(err, "Error closing description file", "path", descriptionFilePath)
			}
		}()

		return ldr.Load(descriptionFile)
	}

	if description != "" {
		return ldr.Load(strings.NewReader(description))
	}

	return ldr.Load(os.Stdin)
}

// createRunnerBuilder creates a new runner builder.
func (cw *CommandWrapper) createRunnerBuilder() (runner.Builder, error) {
	resourceProcessBuilder := processbuilder.New()
	resourceBuilder, err := resbuilder.New(resourceProcessBuilder)
	if err != nil {
		return nil, fmt.Errorf("error creating resource builder: %w", err)
	}

	syscallBuilder := sysbuilder.New()
	stepBuilder, err := stepbuilder.New(syscallBuilder)
	if err != nil {
		return nil, fmt.Errorf("error creating step builder: %w", err)
	}

	testBuilder, err := testbuilder.New(resourceBuilder, stepBuilder)
	if err != nil {
		return nil, fmt.Errorf("error creating test builder: %w", err)
	}

	runnerProcessBuilder := processbuilder.New()

	runnerContainerBuilderOptions := []containerbuilder.Option{
		containerbuilder.WithUnixSocketPath(cw.ContainerRuntimeUnixSocketPath),
		containerbuilder.WithBaseImageName(cw.ContainerBaseImageName),
		containerbuilder.WithBaseImagePullPolicy(cw.ContainerImagePullPolicy),
	}
	runnerContainerBuilder, err := containerbuilder.New(runnerContainerBuilderOptions...)
	if err != nil {
		return nil, fmt.Errorf("error creating container builder: %w", err)
	}

	return runnerbuilder.New(testBuilder, runnerProcessBuilder, runnerContainerBuilder)
}

// createTester creates a new tester.
func (cw *CommandWrapper) createTester(logger logr.Logger) (tester.Tester, error) {
	gRPCRetrieverOptions := []grpcretriever.Option{
		grpcretriever.WithUnixSocketPath(cw.unixSocketPath),
		grpcretriever.WithHostname(cw.hostname),
		grpcretriever.WithPort(cw.port),
		grpcretriever.WithCertFile(cw.certFile),
		grpcretriever.WithKeyFile(cw.keyFile),
		grpcretriever.WithCARootFile(cw.caRootFile),
		grpcretriever.WithPollingTimeout(cw.pollingTimeout),
	}
	grpcRetriever, err := grpcretriever.New(logger, gRPCRetrieverOptions...)
	if err != nil {
		return nil, fmt.Errorf("error creating gRPC retriever: %w", err)
	}

	t := testerimpl.New(grpcRetriever, cw.TestIDEnvKey, testIDIgnorePrefix)
	return t, nil
}

// buildRunnerEnviron creates a list of strings representing the environment, by adding to the current process
// environment all the provided command flags and all the required environment variable needed to enable the runner to
// rerun the current executable with the proper environment configuration.
func (cw *CommandWrapper) buildRunnerEnviron(cmd *cobra.Command) []string {
	environ := os.Environ()
	environ = cw.appendFlags(environ, cmd.PersistentFlags(), cmd.Flags())
	environ = append(environ, fmt.Sprintf("%s=1", cw.DeclarativeEnvKey))
	return environ
}

// appendFlags appends the provided flag sets' flags to environ and returns the updated environ. Works like the builtin
// append function.
func (cw *CommandWrapper) appendFlags(environ []string, flagSets ...*pflag.FlagSet) []string {
	appendFlag := func(flag *pflag.Flag) {
		keyName := envKeyFromFlagName(cw.EnvKeysPrefix, flag.Name)
		environ = append(environ, fmt.Sprintf("%s=%s", keyName, flag.Value.String()))
	}
	for _, flagSet := range flagSets {
		flagSet.VisitAll(appendFlag)
	}
	return environ
}

// newTestID creates a new test ID from the provided test UID.
func newTestID(uid *uuid.UUID) string {
	return fmt.Sprintf("%s%s", testIDIgnorePrefix, uid.String())
}

// ensureProcessChainLeaf ensures the process chain has at least one element. If the user didn't specify anything, add a
// default process to the chain.
func ensureProcessChainLeaf(testDesc *loader.Test) {
	if testDesc.Context == nil {
		testDesc.Context = &loader.TestContext{}
	}
	if len(testDesc.Context.Processes) == 0 && testDesc.Context.Container == nil {
		testDesc.Context.Processes = []loader.ProcessContext{{}}
	}
}

// extractTestUID extracts the test UID from the provided test ID.
func extractTestUID(testID string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimPrefix(testID, testIDIgnorePrefix))
}

// logRunnerError logs the provided runner error using the provided logger.
func logRunnerError(logger logr.Logger, err error) {
	var resBuildErr *test.ResourceBuildError
	var stepBuildErr *test.StepBuildError
	var resCreationErr *test.ResourceCreationError
	var stepRunErr *test.StepBuildError

	switch {
	case errors.As(err, &resBuildErr):
		logger.Error(resBuildErr.Err, "Error building test resource", "resourceName", resBuildErr.ResourceName,
			"resourceIndex", resBuildErr.ResourceIndex)
	case errors.As(err, &stepBuildErr):
		logger.Error(stepBuildErr.Err, "Error building test step", "stepName", stepBuildErr.StepName,
			"stepIndex", stepBuildErr.StepIndex)
	case errors.As(err, &resCreationErr):
		logger.Error(resCreationErr.Err, "Error creating test resource", "resourceName",
			resCreationErr.ResourceName, "resourceIndex", resCreationErr.ResourceIndex)
	case errors.As(err, &stepRunErr):
		logger.Error(stepRunErr.Err, "Error running test step", "stepName", stepRunErr.StepName, "stepIndex",
			stepRunErr.StepIndex)
	default:
		logger.Error(err, "Error running test")
	}
}

// produceReport produces a report for the given test by using the provided tester.
func produceReport(wg *sync.WaitGroup, testr tester.Tester, testUID *uuid.UUID, testDesc *loader.Test,
	reportFmt reportFormat) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		testName, ruleName := testDesc.Name, testDesc.Rule
		report := getReport(testr, testUID, ruleName, &testDesc.ExpectedOutcome)
		report.TestName, report.RuleName = testName, ruleName
		printReport(report, reportFmt)
	}()
}

// getReport retrieves a report for the provided test and rule by leveraging the provided tester.
func getReport(testr tester.Tester, uid *uuid.UUID, rule string,
	expectedOutcome *loader.TestExpectedOutcome) *tester.Report {
	time.Sleep(1 * time.Second)
	for i := 0; i < 3; i++ {
		report := testr.Report(uid, rule, expectedOutcome)
		if !report.Empty() {
			return report
		}

		time.Sleep((4 / (1 << i)) * time.Second)
	}

	return testr.Report(uid, rule, expectedOutcome)
}

// printReport prints the provided report using the provided format.
func printReport(report *tester.Report, reportFmt reportFormat) {
	var encoder tester.ReportEncoder
	switch reportFmt {
	case reportFormatText:
		encoder = textencoder.New(os.Stdout)
	case reportFormatJSON:
		encoder = jsonencoder.New(os.Stdout)
	case reportFormatYAML:
		encoder = yamlencoder.New(os.Stdout)
	default:
		panic(fmt.Sprintf("unsupported report format %v", report))
	}

	// TODO: perform error checking
	_ = encoder.Encode(report)
}
