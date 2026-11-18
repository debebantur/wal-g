package s3_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
	"github.com/wal-g/wal-g/pkg/storages/s3"
	"github.com/wal-g/wal-g/pkg/storages/storage"
	"github.com/wal-g/wal-g/test/mocks"
	"go.uber.org/mock/gomock"
)

func TestOldestVersionRestoresDeletedObjectAcrossPages(t *testing.T) {
	api := mocks.NewMockAPI(gomock.NewController(t))
	newer, older := time.Unix(20, 0), time.Unix(10, 0)
	key := "root/base/backup/tar_partitions/part.tar"
	api.EXPECT().ListObjectVersions(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in *awss3.ListObjectVersionsInput, _ ...func(*awss3.Options)) (*awss3.ListObjectVersionsOutput, error) {
			require.Equal(t, "bucket", aws.ToString(in.Bucket))
			require.Equal(t, key, aws.ToString(in.Prefix))
			if in.KeyMarker == nil {
				return &awss3.ListObjectVersionsOutput{IsTruncated: aws.Bool(true), NextKeyMarker: &key, NextVersionIdMarker: aws.String("new"),
					Versions:      []types.ObjectVersion{{Key: &key, VersionId: aws.String("new"), LastModified: &newer}},
					DeleteMarkers: []types.DeleteMarkerEntry{{Key: &key, VersionId: aws.String("deleted"), IsLatest: aws.Bool(true), LastModified: &newer}}}, nil
			}
			require.Equal(t, "new", aws.ToString(in.VersionIdMarker))
			return &awss3.ListObjectVersionsOutput{Versions: []types.ObjectVersion{
				{Key: &key, VersionId: aws.String("old"), LastModified: &older, Size: aws.Int64(7)},
				{Key: aws.String(key + ".other"), VersionId: aws.String("unrelated"), LastModified: &older},
			}}, nil
		}).AnyTimes()
	api.EXPECT().HeadObject(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
			require.Equal(t, key, aws.ToString(in.Key))
			require.Equal(t, "old", aws.ToString(in.VersionId))
			return &awss3.HeadObjectOutput{VersionId: in.VersionId, ContentLength: aws.Int64(7), LastModified: &older}, nil
		}).AnyTimes()
	api.EXPECT().GetObject(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
			require.Equal(t, key, aws.ToString(in.Key))
			require.Equal(t, "old", aws.ToString(in.VersionId))
			return &awss3.GetObjectOutput{Body: io.NopCloser(strings.NewReader("payload")), ContentLength: aws.Int64(7)}, nil
		})
	root := s3.NewFolder(api, nil, "root", &s3.Config{Bucket: "bucket"})
	view, err := storage.WithOldestVersions(root)
	require.NoError(t, err)
	folder := view.GetSubFolder("base/backup/tar_partitions")
	exists, err := folder.Exists(t.Context(), "part.tar")
	require.NoError(t, err)
	require.True(t, exists)
	obj, err := folder.StatObject(t.Context(), "part.tar")
	require.NoError(t, err)
	require.Equal(t, "old", obj.GetVersionID())
	reader, err := folder.ReadObject(t.Context(), "part.tar")
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "payload", string(data))
}

func TestOldestVersionMissingAndListingError(t *testing.T) {
	for _, listErr := range []error{nil, errors.New("access denied")} {
		name := "missing"
		if listErr != nil {
			name = "listing_error"
		}
		t.Run(name, func(t *testing.T) {
			api := mocks.NewMockAPI(gomock.NewController(t))
			api.EXPECT().ListObjectVersions(gomock.Any(), gomock.Any(), gomock.Any()).Return(&awss3.ListObjectVersionsOutput{}, listErr).AnyTimes()
			folder, err := storage.WithOldestVersions(s3.NewFolder(api, nil, "root", &s3.Config{}))
			require.NoError(t, err)
			exists, err := folder.Exists(t.Context(), "missing")
			require.False(t, exists)
			if listErr != nil {
				require.ErrorIs(t, err, listErr)
			} else {
				require.NoError(t, err)
			}
			_, err = folder.ReadObject(t.Context(), "missing")
			require.Error(t, err)
			if listErr != nil {
				require.ErrorIs(t, err, listErr)
			}
		})
	}
}

func TestOldestVersionListingSelectsOneDataVersionAcrossPages(t *testing.T) {
	api := mocks.NewMockAPI(gomock.NewController(t))
	newer, older := time.Unix(20, 0), time.Unix(10, 0)
	key := "root/sentinel.json"
	page1 := &awss3.ListObjectVersionsOutput{
		IsTruncated: aws.Bool(true), NextKeyMarker: &key, NextVersionIdMarker: aws.String("new"),
		CommonPrefixes: []types.CommonPrefix{{Prefix: aws.String("root/backup/")}},
		Versions:       []types.ObjectVersion{{Key: &key, VersionId: aws.String("new"), LastModified: &newer}},
		DeleteMarkers:  []types.DeleteMarkerEntry{{Key: &key, VersionId: aws.String("deleted"), IsLatest: aws.Bool(true), LastModified: &newer}},
	}
	page2 := &awss3.ListObjectVersionsOutput{
		CommonPrefixes: page1.CommonPrefixes,
		Versions: []types.ObjectVersion{
			{Key: &key, VersionId: aws.String("old"), LastModified: &older, Size: aws.Int64(0)},
			{Key: aws.String("root/"), VersionId: aws.String("directory"), LastModified: &older},
		},
		DeleteMarkers: []types.DeleteMarkerEntry{{Key: aws.String("root/marker-only"), VersionId: aws.String("deleted"), LastModified: &older}},
	}
	gomock.InOrder(
		api.EXPECT().ListObjectVersions(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, in *awss3.ListObjectVersionsInput, _ ...func(*awss3.Options)) (*awss3.ListObjectVersionsOutput, error) {
				require.Equal(t, "root/", aws.ToString(in.Prefix))
				require.Equal(t, "/", aws.ToString(in.Delimiter))
				return page1, nil
			}),
		api.EXPECT().ListObjectVersions(gomock.Any(), gomock.Any(), gomock.Any()).Return(page2, nil),
	)
	folder, err := storage.WithOldestVersions(s3.NewFolder(api, nil, "root", &s3.Config{}))
	require.NoError(t, err)
	objects, children, err := folder.ListFolder(t.Context())
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, "sentinel.json", objects[0].GetName())
	require.Equal(t, "old", objects[0].GetVersionID())
	require.Zero(t, objects[0].GetSize(), "zero-byte data versions are valid objects")
	require.Len(t, children, 1)
	require.Equal(t, "root/backup/", children[0].GetPath())
}
