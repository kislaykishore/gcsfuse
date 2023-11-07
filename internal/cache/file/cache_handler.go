// Copyright 2023 Google Inc. All Rights Reserved.
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

package file

import (
	"fmt"
	"os"

	"github.com/googlecloudplatform/gcsfuse/internal/cache/data"
	"github.com/googlecloudplatform/gcsfuse/internal/cache/file/downloader"
	"github.com/googlecloudplatform/gcsfuse/internal/cache/lru"
	"github.com/googlecloudplatform/gcsfuse/internal/cache/util"
	"github.com/googlecloudplatform/gcsfuse/internal/locker"
	"github.com/googlecloudplatform/gcsfuse/internal/logger"
	"github.com/googlecloudplatform/gcsfuse/internal/storage/gcs"
)

// CacheHandler Responsible for managing fileInfoCache as well as fileDownloadManager.
type CacheHandler struct {
	// fileInfoCache contains the reference of fileInfo cache.
	fileInfoCache *lru.Cache

	// jobManager contains reference to a singleton jobManager.
	jobManager *downloader.JobManager

	// Directory location which would contain local file to keep the downloaded data.
	cacheLocation string

	// filePerm parameter specifies the permission behaviour to determine which users
	// will have access to the cache file.
	filePerm os.FileMode

	// Guards the handling of cache entry insertion while creating the CacheHandle.
	// Also guards post eviction logic E.g. cancelling and deletion of async download job,
	// deletion of local file containing downloaded data.
	mu locker.Locker
}

func NewCacheHandler(fileInfoCache *lru.Cache, jobManager *downloader.JobManager, cacheLocation string, filePerm os.FileMode) *CacheHandler {
	return &CacheHandler{
		fileInfoCache: fileInfoCache,
		jobManager:    jobManager,
		cacheLocation: cacheLocation,
		filePerm:      filePerm,
		mu:            locker.New("FileCacheHandler", func() {}),
	}
}

func (chr *CacheHandler) createLocalFileReadHandle(objectName string, bucketName string) (*os.File, error) {
	fileSpec := data.FileSpec{
		Path: util.GetDownloadPath(chr.cacheLocation, util.GetObjectPath(bucketName, objectName)),
		Perm: chr.filePerm,
	}

	return util.CreateFile(fileSpec, os.O_RDONLY)
}

func (chr *CacheHandler) cleanUpEvictedFile(fileInfo *data.FileInfo) error {
	key := fileInfo.Key
	_, err := key.Key()
	if err != nil {
		return fmt.Errorf("error while performing post eviction: %v", err)
	}

	// Removing Job doesn't delete the job object itself but invalidates the job and
	// hence it's possible that some existing cache handle will have reference to older
	// job object, but that job object will have INVALID status.
	chr.jobManager.RemoveJob(key.ObjectName, key.BucketName)

	localFilePath := util.GetDownloadPath(chr.cacheLocation, util.GetObjectPath(key.BucketName, key.ObjectName))
	err = os.Remove(localFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warnf("while clean up, filePath should exist: %v", err)
		} else {
			return fmt.Errorf("while deleting file: %s, error: %v", localFilePath, err)
		}
	}

	return nil
}

// addFileInfoEntryToCache adds a data.FileInfo entry for the given object and bucket
// in the file info cache if it does not already exist. While adding the entry, it also
// performs the post eviction work (clean up for async job and local cache file) for the
// evicted entry.
// In case if the cache contains the stale data.FileInfo entry (generation < object.generation)
// it cleans up (job and local cache file) for the old entry and adds the new entry with latest
// generation to the cache.

// Acquires and releases LOCK(CacheHandler.mu)
func (chr *CacheHandler) addFileInfoEntryToCache(object *gcs.MinObject, bucket gcs.Bucket) error {
	fileInfoKey := data.FileInfoKey{
		BucketName: bucket.Name(),
		ObjectName: object.Name,
	}
	fileInfoKeyName, err := fileInfoKey.Key()
	if err != nil {
		return fmt.Errorf("while creating key: %v", fileInfoKeyName)
	}

	chr.mu.Lock()
	defer chr.mu.Unlock()

	addEntryToCache := false
	// Todo - (raj-prince) - this lookUp should not change the LRU order.
	fileInfo := chr.fileInfoCache.LookUp(fileInfoKeyName)
	if fileInfo == nil {
		addEntryToCache = true
	} else {
		fileInfoData := fileInfo.(data.FileInfo)
		if fileInfoData.ObjectGeneration < object.Generation {
			erasedVal := chr.fileInfoCache.Erase(fileInfoKeyName)
			if erasedVal != nil {
				fileInfo := erasedVal.(data.FileInfo)
				err := chr.cleanUpEvictedFile(&fileInfo)
				if err != nil {
					return fmt.Errorf("while performing post eviction of %s object error: %v", fileInfo.Key.ObjectName, err)
				}
			}
			addEntryToCache = true
		} else if fileInfoData.ObjectGeneration > object.Generation {
			return fmt.Errorf("cache generation %d is more than object generation: %d", fileInfoData.ObjectGeneration, object.Generation)
		}
	}

	if addEntryToCache {
		fileInfo = data.FileInfo{
			Key:              fileInfoKey,
			ObjectGeneration: object.Generation,
			Offset:           0,
			FileSize:         object.Size,
		}

		evictedValues, err := chr.fileInfoCache.Insert(fileInfoKeyName, fileInfo)
		if err != nil {
			return fmt.Errorf("while inserting into the cache: %v", err)
		}

		for _, val := range evictedValues {
			fileInfo := val.(data.FileInfo)
			err := chr.cleanUpEvictedFile(&fileInfo)
			if err != nil {
				return fmt.Errorf("while performing post eviction of %s object error: %v", fileInfo.Key.ObjectName, err)
			}
		}
	} else {
		job := chr.jobManager.GetJob(object, bucket)
		jobStatus := job.GetStatus()
		if jobStatus.Name != downloader.FAILED {
			// Move this entry on top of LRU.
			_ = chr.fileInfoCache.LookUp(fileInfoKeyName)
		}
	}

	return nil
}

// GetCacheHandle creates an entry in fileInfoCache if it does not already exist.
// It creates downloader.Job if not already exist. Also, creates local file
// which contains the object content. Finally, it returns a CacheHandle that
// contains the reference to downloader.Job and the local file handle.

// Acquires and releases LOCK(CacheHandler.mu)
func (chr *CacheHandler) GetCacheHandle(object *gcs.MinObject, bucket gcs.Bucket, initialOffset int64) (*CacheHandle, error) {
	err := chr.addFileInfoEntryToCache(object, bucket)
	if err != nil {
		return nil, fmt.Errorf("while adding the entry in the cache: %v", err)
	}

	localFileReadHandle, err := chr.createLocalFileReadHandle(object.Name, bucket.Name())
	if err != nil {
		return nil, fmt.Errorf("while create local-file read handle: %v", err)
	}

	return NewCacheHandle(localFileReadHandle, chr.jobManager.GetJob(object, bucket), chr.fileInfoCache, initialOffset), nil
}

// InvalidateCache removes the entry from the fileInfoCache, and removes download job,
// and delete local file in the cache.

// Acquires and releases LOCK(CacheHandler.mu)
func (chr *CacheHandler) InvalidateCache(object *gcs.MinObject, bucket gcs.Bucket) error {
	fileInfoKey := data.FileInfoKey{
		BucketName: bucket.Name(),
		ObjectName: object.Name,
	}
	fileInfoKeyName, err := fileInfoKey.Key()
	if err != nil {
		return fmt.Errorf("while creating key: %v", fileInfoKeyName)
	}

	chr.mu.Lock()
	defer chr.mu.Unlock()

	erasedVal := chr.fileInfoCache.Erase(fileInfoKeyName)
	if erasedVal != nil {
		fileInfo := erasedVal.(data.FileInfo)
		err := chr.cleanUpEvictedFile(&fileInfo)
		if err != nil {
			return fmt.Errorf("while performing post eviction of %s object error: %v", fileInfo.Key.ObjectName, err)
		}
	}
	return nil
}

// Destroy destroys the internal state of CacheHandler correctly specifically evict all the
// entries in the file info cache and clean up for the evicted entries.
// TODO (raj-prince) to implement.
func (chr *CacheHandler) Destroy() {

}
