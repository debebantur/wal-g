#!/bin/bash
set -e -x

CONFIG_FILE="/tmp/configs/partial_restore_test_config.json"
COMMON_CONFIG="/tmp/configs/common_config.json"
TMP_CONFIG="/tmp/configs/tmp_config.json"
cat ${CONFIG_FILE} > ${TMP_CONFIG}
echo "," >> ${TMP_CONFIG}
cat ${COMMON_CONFIG} >> ${TMP_CONFIG}
/tmp/pg_scripts/wrap_config_file.sh ${TMP_CONFIG}
source /tmp/tests/test_functions/util.sh

wal-g --config=${TMP_CONFIG} delete everything FORCE --confirm

bootstrap_gp_cluster
sleep 3
enable_pitr_extension
setup_wal_archiving

# insert_data
n=10000
it=10

psql -p 6000 -c "DROP DATABASE IF EXISTS db"
psql -p 6000 -c "CREATE DATABASE db"
psql -p 6000 -d db -c "CREATE SCHEMA restore"
psql -p 6000 -d db -c "CREATE SCHEMA partial"
psql -p 6000 -d db -c "CREATE SCHEMA skip"

psql -p 6000 -d db -c "CREATE TABLE restore.table1 AS SELECT a FROM generate_series(1,$n) AS a;"
psql -p 6000 -d db -c "CREATE TABLE restore.table2 AS SELECT a FROM generate_series(1,$n) AS a;"
psql -p 6000 -d db -c "CREATE TABLE partial.table1 AS SELECT a FROM generate_series(1,$n) AS a;"
psql -p 6000 -d db -c "CREATE TABLE partial.table2 AS SELECT a FROM generate_series(1,$n) AS a;"
psql -p 6000 -d db -c "CREATE TABLE skip.table1 AS SELECT a FROM generate_series(1,$n) AS a;"
psql -p 6000 -d db -c "CREATE TABLE skip.table2 AS SELECT a FROM generate_series(1,$n) AS a;"

psql -p 6000 -d db -c "INSERT INTO restore.table1 SELECT i FROM generate_series(1,$n)i;"
psql -p 6000 -d db -c "INSERT INTO restore.table2 SELECT i FROM generate_series(1,$n)i;"
psql -p 6000 -d db -c "INSERT INTO partial.table1 SELECT i FROM generate_series(1,$n)i;"
psql -p 6000 -d db -c "INSERT INTO partial.table2 SELECT i FROM generate_series(1,$n)i;"
psql -p 6000 -d db -c "INSERT INTO skip.table1 SELECT i FROM generate_series(1,$n)i;"
psql -p 6000 -d db -c "INSERT INTO skip.table2 SELECT i FROM generate_series(1,$n)i;"

run_backup_logged ${TMP_CONFIG} ${PGDATA}
stop_and_delete_cluster_dir

wal-g --config=${TMP_CONFIG} backup-fetch LATEST --in-place --restore-only=db/restore/*,db/partial/table2

start_cluster

if [ "$(psql -p 6000 -t -c "SELECT count(*) FROM restore.table1;" -d db -A)" != $($n * 2) ]; then
  echo "Error: restore namespace must be restored after partial fetch"
  exit 1
elif [ "$(psql -p 6000 -t -c "SELECT count(*) FROM restore.table2;" -d db -A)" != $($n * 2) ]; then
  echo "Error: restore namespace must be restored after partial fetch"
  exit 1
elif [ "$(psql -p 6000 -t -c "SELECT count(*) FROM partial.table2;" -d db -A)" != $($n * 2) ]; then
  echo "Error: table2 in partial namespace must be restored after partial fetch"
  exit 1
fi

EXPECTED_ERROR_MSG="could not open file"

set +e
output1=$(psql -p 6000 -t -c "SELECT count(*) FROM partial.table1;" -d db -A 2>&1) 
output2=$(psql -p 6000 -t -c "SELECT count(*) FROM skip.table1;" -d db -A 2>&1)
output3=$(psql -p 6000 -t -c "SELECT count(*) FROM skip.table2;" -d db -A 2>&1)
set -e

if ! echo $output1 | grep -q "$EXPECTED_ERROR_MSG"; then
  echo "Error: skip namespace must be emtpy after partial fetch"
  echo $output1
  exit 1
elif ! echo $output2 | grep -q "$EXPECTED_ERROR_MSG"; then
  echo "Error: skip namespace must be emtpy after partial fetch"
  echo $output2
  exit 1
elif ! echo $output3 | grep -q "$EXPECTED_ERROR_MSG"; then
  echo "Error: to_skip database directory must be emtpy after partial fetch"
  echo $output3
  exit 1
fi

cleanup