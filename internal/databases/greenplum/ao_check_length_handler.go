package greenplum

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx"
	"github.com/jackc/pgx/pgtype"
	"github.com/wal-g/tracelog"
	"github.com/wal-g/wal-g/internal/databases/postgres"
)

type DBInfo struct {
	DBName string
	Oid    pgtype.OID
}

type RelNames struct {
	FileName   string
	TableName  string
	SegRelName string
	Size       int64
}

func /*(some handler)*/ CheckWTF(port, segnum string) {

	conn1, err := postgres.Connect(func(config *pgx.ConnConfig) error {
		a, err := strconv.Atoi(port)
		if err != nil {
			return err
		}
		config.Port = uint16(a)
		return nil
	})
	if err != nil {
		tracelog.ErrorLogger.FatalfOnError("unable to get connection %v", err)
	}

	DBNames, err := GetDatabaseConnections(conn1)
	if err != nil {
		tracelog.ErrorLogger.FatalfOnError("unable to list databases %v", err)
	}

	err = conn1.Close()
	if err != nil {
		tracelog.WarningLogger.Println("failed close conn")
	}

	for _, db := range DBNames {
		tracelog.DebugLogger.Println(db.DBName)
		conn, err := postgres.Connect(func(config *pgx.ConnConfig) error {
			a, err := strconv.Atoi(port)
			if err != nil {
				return err
			}
			config.Port = uint16(a)
			config.Database = db.DBName
			return nil
		})
		if err != nil {
			tracelog.ErrorLogger.FatalfOnError("unable to get connection %v", err)
		}

		rows, err := conn.Query(`SELECT a.relfilenode file, a.relname tname, b.relname segname 
	FROM (SELECT relname, relid, segrelid, relpersistence, relfilenode FROM pg_class JOIN pg_appendonly ON oid = relid) a,
	(SELECT relname, segrelid FROM pg_class JOIN pg_appendonly ON oid = segrelid) b
	WHERE a.relpersistence = 'p' AND a.segrelid = b.segrelid;`)

		if err != nil {
			tracelog.ErrorLogger.FatalfOnError("unable to get ao/aocs tables %v", err)
		}
		defer rows.Close()

		mas := make([]RelNames, 0)
		for rows.Next() {
			row := RelNames{}
			if err := rows.Scan(&row.FileName, &row.TableName, &row.SegRelName); err != nil {
				tracelog.ErrorLogger.FatalfOnError("unable to parse query output %v", err)
			}
			mas = append(mas, row)
		}

		tracelog.DebugLogger.Printf("mas size: %d", len(mas))
		tracelog.DebugLogger.Printf("mas: %v", mas)

		relNames := make(map[string]RelNames, 0)
		for _, v := range mas {
			v.Size, err = GetTableMetadataEOF(v, conn)
			if err != nil {
				tracelog.ErrorLogger.FatalfOnError("unable to get table metadata %v", err)
			}
			relNames[v.FileName] = v
			tracelog.DebugLogger.Printf("table: %s size: %d", v.TableName, v.Size)
		}

		tracelog.DebugLogger.Printf("relations size: %d", len(relNames))
		tracelog.DebugLogger.Printf("relations: %v", relNames)

		entries, err := os.ReadDir(fmt.Sprintf("/var/lib/greenplum/data1/primary/%s/base/%d/", fmt.Sprintf("gpseg%s", segnum), db.Oid))
		if err != nil {
			tracelog.ErrorLogger.FatalfOnError("unable to list tables` file directory %v", err)
		}
		tracelog.DebugLogger.Printf("entries num: %d", len(entries))
		tracelog.DebugLogger.Printf("was in: %s", fmt.Sprintf("/var/lib/greenplum/data1/primary/gpseg%s/base/%d/", segnum, db.Oid))

		for _, e := range entries {
			tracelog.DebugLogger.Printf("was entry: %v", e)
			parts := strings.Split(e.Name(), ".")
			f, err := e.Info()
			if err != nil {
				tracelog.ErrorLogger.FatalfOnError("unable to get file data %v", err)
			}
			if !f.IsDir() {
				tem, ok := relNames[parts[0]]
				if !ok {
					tracelog.WarningLogger.Printf("no metadata for file %s", parts[0])
					continue
				}
				tracelog.DebugLogger.Printf("was table: %s size: %d", tem.TableName, tem.Size)
				tem.Size -= f.Size()
				relNames[parts[0]] = tem
				tracelog.DebugLogger.Printf("now table: %s size: %d", relNames[parts[0]].TableName, relNames[parts[0]].Size)
			}
		}

		tracelog.DebugLogger.Printf("map size: %d", len(relNames))
		for _, v := range relNames {
			tracelog.DebugLogger.Printf("element: %+v", v)
			if v.Size > 0 {
				tracelog.ErrorLogger.Fatalf("file for table %s is shorter than expected for %d", v.TableName, v.Size)
			}
		}
		err = conn.Close()
		if err != nil {
			tracelog.WarningLogger.Println("failed close conn")
		}
	}

}

func GetDatabaseConnections(conn *pgx.Conn) ([]DBInfo, error) {
	rows, err := conn.Query("SELECT datname, oid FROM pg_database WHERE datallowconn")
	if err != nil {
		return nil, err
	}
	tracelog.DebugLogger.Printf("raw data: %v", rows)
	names := make([]DBInfo, 0)
	for rows.Next() {
		tem := DBInfo{}
		if err = rows.Scan(&tem.DBName, &tem.Oid); err != nil {
			return nil, err
		}
		tracelog.DebugLogger.Printf("existing table: %s oid: %d", tem.DBName, tem.Oid)
		names = append(names, tem)
	}

	return names, nil
}

func GetTableMetadataEOF(row RelNames, conn *pgx.Conn) (int64, error) {
	query := ""
	if !strings.Contains(row.SegRelName, "aocs") {
		query = fmt.Sprintf("SELECT sum(eofuncompressed) FROM pg_aoseg.%s", row.SegRelName)
	} else {
		query = fmt.Sprintf("SELECT sum(eof_uncompressed) FROM gp_toolkit.__gp_aocsseg('\"%s\"')", row.TableName)
	}

	// get expected size of table in metadata
	size, err := conn.Query(query)
	if err != nil {
		return 0, err
	}
	defer size.Close()
	var metaEOF int64
	for size.Next() {
		err = size.Scan(&metaEOF)
		if err != nil {
			metaEOF = int64(0)
		}
	}
	return metaEOF, nil
}
