// Copyright 2023 Google LLC
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

package storageutil

import (
	"github.com/googlecloudplatform/gcsfuse/v2/internal/storage/gcs"
	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
)

// Delete all objects from the supplied bucket. Results are undefined if the
// bucket is being concurrently updated.
func DeleteAllObjects(
	ctx context.Context,
	bucket gcs.Bucket) error {
	group, derivedContext := errgroup.WithContext(ctx)

	// List all of the objects in the bucket.
	objects := make(chan *gcs.Object, 100)
	group.Go(func() error {
		defer close(objects)
		return ListPrefix(derivedContext, bucket, "", objects)
	})

	// Strip everything but the name.
	objectNames := make(chan string, 10e3)
	group.Go(func() (err error) {
		defer close(objectNames)
		for o := range objects {
			select {
			case <-derivedContext.Done():
				err = derivedContext.Err()
				return

			case objectNames <- o.Name:
			}
		}

		return
	})

	// Delete the objects in parallel.
	const parallelism = 64
	for i := 0; i < parallelism; i++ {
		group.Go(func() error {
			for objectName := range objectNames {
				err := bucket.DeleteObject(
					derivedContext,
					&gcs.DeleteObjectRequest{
						Name: objectName,
					})

				if err != nil {
					return err
				}
			}

			return nil
		})
	}

	return group.Wait()
}
