#!/bin/bash
set -e -x

pushd /tmp

for i in tests/*.sh; do
  continue
  # Exclude pgbackrest because it needs to be run in separate container. It should be run individually.
  if [ "$i" = "tests/pgbackrest_backup_fetch_test.sh" ]; then continue ;fi
  # Exclude ssh test because of ssh server container dependency.
  if [ "$i" = "tests/ssh_backup_test.sh" ]; then continue ;fi
  # Exclude throttling test because of different s3 container for throttling.
  if [ "$i" = "tests/wal_perftest_with_throttling.sh" ]; then continue ;fi
  echo
  echo "===== RUNNING $i ====="
  set -x
  
  ./"$i";

  set +x
  echo "===== SUCCESS $i ====="
  echo

done
popd
