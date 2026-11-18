package postgres

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
	"github.com/wal-g/wal-g/internal"
	"github.com/wal-g/wal-g/internal/multistorage"
	"github.com/wal-g/wal-g/internal/multistorage/policies"
	"github.com/wal-g/wal-g/internal/multistorage/stats"
	walgs3 "github.com/wal-g/wal-g/pkg/storages/s3"
	"github.com/wal-g/wal-g/pkg/storages/storage"
	"github.com/wal-g/wal-g/test/mocks"
	"github.com/wal-g/wal-g/utility"
	"go.uber.org/mock/gomock"
)

// Exercise the actual PostgreSQL extraction paths with deleted S3 sentinel and
// tar objects. This is an SDK mock fixture, not a running PostgreSQL cluster.
func TestBackupFetchOldestVersions(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "forward"
		if reverse {
			name = "reverse"
		}
		t.Run(name, func(t *testing.T) {
			originalUnwrap := useNewUnwrapImplementation
			t.Cleanup(func() { useNewUnwrapImplementation = originalUnwrap })
			useNewUnwrapImplementation = false
			ctrl := gomock.NewController(t)
			api := mocks.NewMockAPI(ctrl)
			backupName := "base_000000010000000000000001"
			deltaName := "base_000000010000000000000002_D_000000010000000000000001"
			prefix := "archive/" + utility.BaseBackupPath
			makeTar := func(name, content string) []byte {
				var buf bytes.Buffer
				tw := tar.NewWriter(&buf)
				require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(content))}))
				_, err := io.WriteString(tw, content)
				require.NoError(t, err)
				require.NoError(t, tw.Close())
				return buf.Bytes()
			}
			objects := map[string][]byte{
				prefix + backupName + "/" + FilesMetadataName:          []byte(`{"Files":{"base/123/456":{},"global/pg_control":{}}}`),
				prefix + internal.SentinelNameFromBackup(backupName):   []byte(`{"LSN":1}`),
				prefix + backupName + "/tar_partitions/part_001.tar":   makeTar("base/123/456", "original table data"),
				prefix + backupName + "/tar_partitions/pg_control.tar": makeTar("global/pg_control", "original control data"),
			}
			deltaSentinel, err := json.Marshal(BackupSentinelDto{
				BackupStartLSN: new(LSN(2)), IncrementFromLSN: new(LSN(1)),
				IncrementFrom: &backupName, IncrementFullName: &backupName, IncrementCount: new(1),
			})
			require.NoError(t, err)
			objects[prefix+internal.SentinelNameFromBackup(deltaName)] = deltaSentinel
			objects[prefix+deltaName+"/"+FilesMetadataName] = []byte(`{"Files":{"base/123/456":{"IsSkipped":true},"delta.txt":{},"global/pg_control":{}}}`)
			objects[prefix+deltaName+"/tar_partitions/part_001.tar"] = makeTar("delta.txt", "delta data")
			objects[prefix+deltaName+"/tar_partitions/pg_control.tar"] = makeTar("global/pg_control", "original control data")

			when := time.Unix(10, 0)
			api.EXPECT().ListObjectVersions(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, in *awss3.ListObjectVersionsInput, _ ...func(*awss3.Options)) (*awss3.ListObjectVersionsOutput, error) {
					out := &awss3.ListObjectVersionsOutput{}
					keys := make([]string, 0, len(objects))
					for key := range objects {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					for _, key := range keys {
						if !strings.HasPrefix(key, aws.ToString(in.Prefix)) {
							continue
						}
						rest := strings.TrimPrefix(key, aws.ToString(in.Prefix))
						if aws.ToString(in.Delimiter) != "" && strings.Contains(rest, "/") {
							continue
						}
						modified := when
						if strings.Contains(key, deltaName) {
							modified = when.Add(time.Second)
						}
						out.Versions = append(out.Versions, types.ObjectVersion{Key: aws.String(key), VersionId: aws.String("old"), LastModified: &modified, Size: aws.Int64(int64(len(objects[key])))})
						out.DeleteMarkers = append(out.DeleteMarkers, types.DeleteMarkerEntry{Key: aws.String(key), VersionId: aws.String("deleted"), IsLatest: aws.Bool(true), LastModified: &when})
					}
					return out, nil
				}).AnyTimes()
			api.EXPECT().HeadObject(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, in *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
					data, ok := objects[aws.ToString(in.Key)]
					if !ok || in.VersionId == nil {
						return nil, &types.NotFound{}
					}
					require.Equal(t, "old", aws.ToString(in.VersionId))
					return &awss3.HeadObjectOutput{VersionId: in.VersionId, ContentLength: aws.Int64(int64(len(data))), LastModified: &when}, nil
				}).AnyTimes()
			api.EXPECT().GetObject(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, in *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
					require.Equal(t, "old", aws.ToString(in.VersionId))
					data, ok := objects[aws.ToString(in.Key)]
					require.True(t, ok, aws.ToString(in.Key))
					return &awss3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data)), ContentLength: aws.Int64(int64(len(data)))}, nil
				}).AnyTimes()
			s3Folder := walgs3.NewFolder(api, nil, "archive", &walgs3.Config{Bucket: "test"})
			collector := stats.NewMockCollector(ctrl)
			collector.EXPECT().SpecificStorage(gomock.Any(), "s3").Return(true, nil).AnyTimes()
			collector.EXPECT().ReportOperationResult(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			root := multistorage.SetPolicies(multistorage.NewFolder(map[string]storage.Folder{"s3": s3Folder}, collector), policies.UniteAllStorages)
			root, err = multistorage.UseSpecificStorage(t.Context(), "s3", root)
			require.NoError(t, err)
			_, err = internal.GetBackupByName(t.Context(), backupName, utility.BaseBackupPath, root)
			require.Error(t, err, "deleted backup must be invisible to regular restore")
			view, err := storage.WithOldestVersions(root)
			require.NoError(t, err)
			for _, selection := range []string{backupName, deltaName, internal.LatestString} {
				backup, err := internal.GetBackupByName(t.Context(), selection, utility.BaseBackupPath, view)
				require.NoError(t, err)
				expectedName := selection
				if selection == internal.LatestString {
					expectedName = deltaName
				}
				require.Equal(t, expectedName, backup.Name)
				pgBackup := ToPgBackup(backup)
				files, err := pgBackup.GetFilesToUnwrap(t.Context(), "")
				require.NoError(t, err)
				dest := t.TempDir()
				if reverse {
					err = deltaFetchRecursionNew(t.Context(), NewFetchConfig(dest, pgBackup, view, nil, files, false, ExtractProviderImpl{}))
				} else {
					err = deltaFetchRecursionOld(t.Context(), pgBackup, view, dest, nil, files, ExtractProviderImpl{})
				}
				require.NoError(t, err)
				if expectedName == deltaName {
					data, err := os.ReadFile(filepath.Join(dest, "delta.txt"))
					require.NoError(t, err)
					require.Equal(t, "delta data", string(data))
				}
				for path, expected := range map[string]string{"base/123/456": "original table data", "global/pg_control": "original control data"} {
					data, err := os.ReadFile(filepath.Join(dest, path))
					require.NoError(t, err)
					require.Equal(t, expected, string(data))
				}
			}
			// Creating the view must not change ordinary reads sharing the same client.
			_, err = internal.GetBackupByName(t.Context(), backupName, utility.BaseBackupPath, root)
			require.Error(t, err)
		})
	}
}
