// Copyright 2024 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package read_cache

import (
	"context"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/googlecloudplatform/gcsfuse/tools/integration_tests/util/client"
	"github.com/googlecloudplatform/gcsfuse/tools/integration_tests/util/log_parser/json_parser/read_logs"
	"github.com/googlecloudplatform/gcsfuse/tools/integration_tests/util/setup"
	"github.com/googlecloudplatform/gcsfuse/tools/integration_tests/util/test_setup"
)

////////////////////////////////////////////////////////////////////////
// Boilerplate
////////////////////////////////////////////////////////////////////////

type readOnlyTest struct {
	flags         []string
	storageClient *storage.Client
	ctx           context.Context
}

func (s *readOnlyTest) Setup(t *testing.T) {
	Setup(s.flags, s.ctx, s.storageClient, testDirName)
}

func (s *readOnlyTest) Teardown(t *testing.T) {
	TearDown()
}

////////////////////////////////////////////////////////////////////////
// Helper functions
////////////////////////////////////////////////////////////////////////

func readMultipleFiles(numFiles int, ctx context.Context, storageClient *storage.Client, fileNames []string, fileSize int64, t *testing.T) (expectedOutcome []*Expected) {
	for i := 0; i < numFiles; i++ {
		expectedOutcome = append(expectedOutcome, readFileAndValidateCacheWithGCS(ctx, storageClient, fileNames[i], fileSize, t))
	}
	return expectedOutcome
}

func validateCacheOfMultipleObjectsUsingStructuredLogs(startIndex int, numFiles int, expectedOutcome []*Expected, structuredReadLogs []*read_logs.StructuredReadLogEntry, cacheHit bool, t *testing.T) {
	endIndex := startIndex + numFiles

	for i := startIndex; i < endIndex; i++ {
		validate(expectedOutcome[i], structuredReadLogs[i], true, cacheHit, chunksRead, t)
	}
}

////////////////////////////////////////////////////////////////////////
// Test scenarios
////////////////////////////////////////////////////////////////////////

func (s *readOnlyTest) TestSecondSequentialReadIsCacheHit(t *testing.T) {
	testFileName := testFileName + setup.GenerateRandomString(testFileNameSuffixLength)
	client.SetupFileInTestDirectory(s.ctx, s.storageClient, testDirName, testFileName, fileSize, t)

	// Read file 1st time.
	expectedOutcome1 := readFileAndValidateCacheWithGCS(s.ctx, s.storageClient, testFileName, fileSize, t)
	// Read file 2nd time.
	expectedOutcome2 := readFileAndValidateCacheWithGCS(s.ctx, s.storageClient, testFileName, fileSize, t)

	// Parse the log file and validate cache hit or miss from the structured logs.
	structuredReadLogs := read_logs.GetStructuredLogsSortedByTimestamp(setup.LogFile(), t)
	validate(expectedOutcome1, structuredReadLogs[0], true, false, chunksRead, t)
	validate(expectedOutcome2, structuredReadLogs[1], true, true, chunksRead, t)
}

func (s *readOnlyTest) TestReadFileLargerThanCacheCapacity(t *testing.T) {
	// Set up a file in test directory of size more than cache capacity.
	client.SetupFileInTestDirectory(s.ctx, s.storageClient, testDirName,
		largeFileName, largeFileSize, t)

	// Read file 1st time.
	expectedOutcome1 := ReadFileAndValidateFileIsNotCached(s.ctx, s.storageClient, largeFileName, t)
	// Read file 2nd time.
	expectedOutcome2 := ReadFileAndValidateFileIsNotCached(s.ctx, s.storageClient, largeFileName, t)

	// Parse the log file and validate cache hit or miss from the structured logs.
	structuredReadLogs := read_logs.GetStructuredLogsSortedByTimestamp(setup.LogFile(), t)
	validate(expectedOutcome1, structuredReadLogs[0], true, false, largeFileChunksRead, t)
	validate(expectedOutcome2, structuredReadLogs[1], true, false, largeFileChunksRead, t)
}

func (s *readOnlyTest) TestReadMultipleFilesMoreThanCacheLimit(t *testing.T) {
	fileNames := client.CreateNFilesInDir(s.ctx, s.storageClient, NumberOfFilesMoreThanCacheLimit, testFileName, fileSize, testDirName, t)

	expectedOutcome := readMultipleFiles(NumberOfFilesMoreThanCacheLimit, s.ctx, s.storageClient, fileNames, fileSize, t)
	expectedOutcome = append(expectedOutcome, readMultipleFiles(NumberOfFilesMoreThanCacheLimit, s.ctx, s.storageClient, fileNames, fileSize, t)...)

	// Parse the log file and validate cache hit or miss from the structured logs.
	structuredReadLogs := read_logs.GetStructuredLogsSortedByTimestamp(setup.LogFile(), t)
	validateCacheOfMultipleObjectsUsingStructuredLogs(0, NumberOfFilesMoreThanCacheLimit, expectedOutcome, structuredReadLogs, false, t)
	validateCacheOfMultipleObjectsUsingStructuredLogs(NumberOfFilesMoreThanCacheLimit, NumberOfFilesMoreThanCacheLimit, expectedOutcome, structuredReadLogs, false, t)
}

func (s *readOnlyTest) TestReadMultipleFilesWithinCacheLimit(t *testing.T) {
	fileNames := client.CreateNFilesInDir(s.ctx, s.storageClient, NumberOfFilesWithinCacheLimit, testFileName, fileSize, testDirName, t)

	expectedOutcome := readMultipleFiles(NumberOfFilesWithinCacheLimit, s.ctx, s.storageClient, fileNames, fileSize, t)
	expectedOutcome = append(expectedOutcome, readMultipleFiles(NumberOfFilesWithinCacheLimit, s.ctx, s.storageClient, fileNames, fileSize, t)...)

	// Parse the log file and validate cache hit or miss from the structured logs.
	structuredReadLogs := read_logs.GetStructuredLogsSortedByTimestamp(setup.LogFile(), t)
	validateCacheOfMultipleObjectsUsingStructuredLogs(0, NumberOfFilesWithinCacheLimit, expectedOutcome, structuredReadLogs, false, t)
	validateCacheOfMultipleObjectsUsingStructuredLogs(NumberOfFilesWithinCacheLimit, NumberOfFilesWithinCacheLimit, expectedOutcome, structuredReadLogs, true, t)
}

func (s *readOnlyTest) TestReadFileAfterRemountWillCacheMiss(t *testing.T) {
	testFileName := testFileName + setup.GenerateRandomString(4)
	// Set up a file in test directory of size more than cache capacity.
	client.SetupFileInTestDirectory(s.ctx, s.storageClient, testDirName,
		testFileName, fileSize, t)

	// Read file 1st time.
	expectedOutcome1 := readFileAndValidateCacheWithGCS(s.ctx, s.storageClient, testFileName, fileSize, t)
	// Parse the log file and validate cache hit or miss from the structured logs.
	structuredReadLogs := read_logs.GetStructuredLogsSortedByTimestamp(setup.LogFile(), t)
	validate(expectedOutcome1, structuredReadLogs[0], true, false, chunksRead, t)

	// Unmount the bucket.
	setup.SetMntDir(rootDir)
	unmountGCSFuseAndDeleteLogFile()

	// Validate file is not cached
	validateFileIsNotCached(testFileName, t)

	// Remount
	mountGCSFuse(s.flags)
	setup.SetMntDir(mountDir)

	// Read file 2nd time.
	expectedOutcome2 := readFileAndValidateCacheWithGCS(s.ctx, s.storageClient, testFileName, fileSize, t)

	structuredReadLogs = read_logs.GetStructuredLogsSortedByTimestamp(setup.LogFile(), t)
	validate(expectedOutcome2, structuredReadLogs[0], true, false, chunksRead, t)
}

////////////////////////////////////////////////////////////////////////
// Test Function (Runs once before all tests)
////////////////////////////////////////////////////////////////////////

func TestReadOnlyTest(t *testing.T) {
	// Define flag set to run the tests.
	mountConfigFilePath := createConfigFile(cacheCapacityInMB)
	flagSet := [][]string{
		{"--implicit-dirs=true", "--config-file=" + mountConfigFilePath},
		{"--implicit-dirs=false", "--config-file=" + mountConfigFilePath},
	}

	// Create storage client before running tests.
	ts := &readOnlyTest{ctx: context.Background()}
	closeStorageClient := createStorageClient(t, &ts.ctx, &ts.storageClient)
	defer closeStorageClient()

	// Run tests.
	for _, flags := range flagSet {
		// Run tests without ro flag.
		ts.flags = flags
		test_setup.RunTests(t, ts)
		// Run tests with ro flag.
		ts.flags = append(flags, "--o=ro")
		test_setup.RunTests(t, ts)
	}
}
