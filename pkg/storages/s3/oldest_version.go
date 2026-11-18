package s3

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/pkg/errors"
	"github.com/wal-g/wal-g/pkg/storages/storage"
)

// WithOldestVersions returns an independent view; normal users of the same
// client/configuration continue reading current objects.
func (folder *Folder) WithOldestVersions() (storage.Folder, error) {
	view := *folder
	view.oldestVersions = true
	return &view, nil
}

func olderVersion(candidate storage.Object, previous storage.Object) bool {
	// ListObjectVersions returns versions newest first. On equal timestamps keep
	// the later entry, since S3 version IDs do not encode chronological order.
	return previous == nil || !candidate.GetLastModified().After(previous.GetLastModified())
}

func (folder *Folder) versionObject(v types.ObjectVersion) storage.Object {
	return storage.NewLocalObjectWithVersion(strings.TrimPrefix(aws.ToString(v.Key), folder.path),
		aws.ToTime(v.LastModified), aws.ToInt64(v.Size), aws.ToString(v.VersionId), "")
}

// Point reads scan only versions under the exact object's key, never its parent
// directory. Memory is constant; cost is proportional to retained key versions
// (plus at most one page of adjacent keys), once per read/stat/exists operation.
func (folder *Folder) oldestObject(ctx context.Context, name string) (storage.Object, error) {
	key := folder.path + name
	paginator := s3.NewListObjectVersionsPaginator(folder.s3API, &s3.ListObjectVersionsInput{
		Bucket: folder.bucket, Prefix: aws.String(key), Delimiter: aws.String("/"),
	})
	var oldest storage.Object
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, errors.Wrapf(err, "list versions of S3 object %s", key)
		}
		passedKey := false
		for _, marker := range page.DeleteMarkers {
			passedKey = passedKey || aws.ToString(marker.Key) > key
		}
		for _, prefix := range page.CommonPrefixes {
			passedKey = passedKey || aws.ToString(prefix.Prefix) > key
		}
		for _, v := range page.Versions {
			passedKey = passedKey || aws.ToString(v.Key) > key
			if aws.ToString(v.Key) != key {
				continue
			}
			obj := folder.versionObject(v)
			if olderVersion(obj, oldest) {
				oldest = obj
			}
		}
		// Versions are grouped by key in lexical order. Once a subsequent key
		// appears, no later page can contain versions of the requested object.
		if passedKey {
			break
		}
	}
	return oldest, nil
}

// Restore lists immediate children only: typically backup sentinels or one
// backup's tar partitions. Retain one object per key across all version pages.
func (folder *Folder) listOldestVersions(ctx context.Context) ([]storage.Object, []storage.Folder, error) {
	paginator := s3.NewListObjectVersionsPaginator(folder.s3API, &s3.ListObjectVersionsInput{
		Bucket: folder.bucket, Prefix: aws.String(folder.path), Delimiter: aws.String("/"),
	})
	oldest := make(map[string]storage.Object)
	var order []string
	var children []storage.Folder
	seen := make(map[string]bool)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "list oldest S3 versions in %s", folder.path)
		}
		for _, p := range page.CommonPrefixes {
			name := strings.TrimPrefix(aws.ToString(p.Prefix), folder.path)
			if !seen[name] {
				children = append(children, folder.GetSubFolder(name))
				seen[name] = true
			}
		}
		for _, v := range page.Versions {
			if aws.ToString(v.Key) == folder.path {
				continue
			}
			obj := folder.versionObject(v)
			name := obj.GetName()
			if oldest[name] == nil {
				order = append(order, name)
			}
			if olderVersion(obj, oldest[name]) {
				oldest[name] = obj
			}
		}
	}
	objects := make([]storage.Object, 0, len(oldest))
	for _, name := range order {
		objects = append(objects, oldest[name])
	}
	return objects, children, nil
}
