package interpreter

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/codong-lang/codong/stdlib/codongerror"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// DbModuleObject is the singleton `db` module.
type DbModuleObject struct {
	db          *sql.DB
	scheme      string            // "sqlite", "mysql", "postgres"
	connections map[string]*dbConn // named connections for multi-datasource
	defaultName string            // name of the default connection
}

type dbConn struct {
	db     *sql.DB
	scheme string
	name   string
}

func (d *DbModuleObject) Type() string    { return "module" }
func (d *DbModuleObject) Inspect() string { return "<module:db>" }

// DbUsingObject represents a db.using("name") reference to a specific datasource.
type DbUsingObject struct {
	conn *dbConn
}

func (u *DbUsingObject) Type() string    { return "db_using" }
func (u *DbUsingObject) Inspect() string { return "<db:using>" }

var dbModuleSingleton = &DbModuleObject{
	connections: make(map[string]*dbConn),
}

// getActiveConn returns the active database connection (default).
func (d *DbModuleObject) getActiveConn() *dbConn {
	if d.defaultName != "" {
		if c, ok := d.connections[d.defaultName]; ok {
			return c
		}
	}
	if d.db != nil {
		return &dbConn{db: d.db, scheme: d.scheme, name: "default"}
	}
	return nil
}

// evalDbModuleMethod dispatches db.xxx() calls.
func (interp *Interpreter) evalDbModuleMethod(method string) Object {
	return &BuiltinFunction{
		Name: "db." + method,
		Fn: func(i *Interpreter, args ...Object) Object {
			switch method {
			case "connect":
				return i.dbConnect(args)
			case "disconnect":
				return i.dbDisconnect()
			case "find":
				return i.dbFind(args, false)
			case "find_one":
				return i.dbFind(args, true)
			case "count":
				return i.dbCount(args)
			case "insert":
				return i.dbInsert(args)
			case "update":
				return i.dbUpdate(args)
			case "delete":
				return i.dbDelete(args)
			case "query":
				return i.dbQuery(args)
			case "query_one":
				return i.dbQueryOne(args)
			case "ping":
				return i.dbPing()
			case "stats":
				return i.dbStats()
			case "using":
				return i.dbUsing(args)
			case "transaction":
				return i.dbTransaction(args)
			case "migrate":
				return i.dbMigrate(args)
			case "migration_status":
				return i.dbMigrationStatus()
			case "last_insert_id":
				return i.dbLastInsertID()
			case "pg_copy":
				return i.dbPgCopy(args)
			default:
				return newRuntimeError(codongerror.E2003_QUERY_ERROR,
					fmt.Sprintf("unknown db method: %s", method), "")
			}
		},
	}
}

// extractScheme parses URL scheme from a DSN string.
func extractScheme(dsn string) string {
	if strings.HasPrefix(dsn, "mysql://") {
		return "mysql"
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return "postgres"
	}
	if strings.HasPrefix(dsn, "sqlite://") {
		return "sqlite"
	}
	// Default to sqlite for backward compatibility
	return "sqlite"
}

// normalizeDSN converts Codong DSN to the format expected by each Go driver.
func normalizeDSN(dsn string, scheme string, opts *MapObject) string {
	switch scheme {
	case "mysql":
		return normalizeMySQLDSN(dsn, opts)
	case "postgres":
		return normalizePostgresDSN(dsn, opts)
	case "sqlite":
		return normalizeSQLiteDSN(dsn)
	}
	return dsn
}

func normalizeMySQLDSN(dsn string, opts *MapObject) string {
	// mysql://user:pass@host:port/dbname?params → user:pass@tcp(host:port)/dbname?params
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "3306"
	}
	dbname := strings.TrimPrefix(u.Path, "/")
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	// Allow overriding from options map
	if opts != nil {
		if v, ok := opts.Entries["username"]; ok {
			user = v.Inspect()
		}
		if v, ok := opts.Entries["password"]; ok {
			pass = v.Inspect()
		}
		if v, ok := opts.Entries["database"]; ok {
			dbname = v.Inspect()
		}
	}

	result := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", user, pass, host, port, dbname)

	// Pass through query params
	params := u.Query()
	if len(params) > 0 {
		result += "?" + params.Encode()
	} else {
		result += "?parseTime=true"
	}

	return result
}

func normalizePostgresDSN(dsn string, opts *MapObject) string {
	// lib/pq accepts postgres:// URLs directly, but we handle password override
	if opts != nil {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		user := ""
		pass := ""
		if u.User != nil {
			user = u.User.Username()
			pass, _ = u.User.Password()
		}
		if v, ok := opts.Entries["username"]; ok {
			user = v.Inspect()
		}
		if v, ok := opts.Entries["password"]; ok {
			pass = v.Inspect()
		}
		if v, ok := opts.Entries["database"]; ok {
			u.Path = "/" + v.Inspect()
		}
		u.User = url.UserPassword(user, pass)
		return u.String()
	}
	return dsn
}

func normalizeSQLiteDSN(dsn string) string {
	if strings.HasPrefix(dsn, "sqlite:///") {
		return dsn[len("sqlite:///"):]
	}
	if strings.HasPrefix(dsn, "sqlite://") {
		return dsn[len("sqlite://"):]
	}
	return dsn
}

// normalizeQueryPlaceholders converts ? placeholders to $1,$2,... for PostgreSQL.
// Uses a state machine to avoid replacing ? inside quotes or JSONB operators.
func normalizeQueryPlaceholders(query string, scheme string) string {
	if scheme != "postgres" {
		return query
	}

	var result strings.Builder
	n := 1
	inSingleQuote := false
	inDoubleQuote := false

	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]

		// Track quote state
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			result.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			result.WriteRune(ch)
			continue
		}

		// Inside quotes, output as-is
		if inSingleQuote || inDoubleQuote {
			result.WriteRune(ch)
			continue
		}

		// Double ?? → single ? (JSONB escape)
		if ch == '?' && i+1 < len(runes) && runes[i+1] == '?' {
			result.WriteRune('?')
			i++
			continue
		}

		// JSONB operators ?| and ?& → preserve
		if ch == '?' && i+1 < len(runes) && (runes[i+1] == '|' || runes[i+1] == '&') {
			result.WriteRune('?')
			continue
		}

		// Regular ? → $N
		if ch == '?' {
			result.WriteString(fmt.Sprintf("$%d", n))
			n++
			continue
		}

		result.WriteRune(ch)
	}
	return result.String()
}

// expandArrayParams expands array parameters into multiple placeholders for IN clauses.
func expandArrayParams(query string, args []interface{}) (string, []interface{}) {
	var resultQuery strings.Builder
	var resultArgs []interface{}
	paramIdx := 0

	runes := []rune(query)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch != '?' {
			resultQuery.WriteRune(ch)
			continue
		}

		if paramIdx < len(args) {
			arg := args[paramIdx]
			paramIdx++

			// Check if argument is a list (from Codong ListObject)
			if arr, ok := arg.([]interface{}); ok {
				if len(arr) == 0 {
					// Empty array: use NULL to return empty result
					resultQuery.WriteString("NULL")
				} else {
					for j, elem := range arr {
						if j > 0 {
							resultQuery.WriteRune(',')
						}
						resultQuery.WriteRune('?')
						resultArgs = append(resultArgs, elem)
					}
				}
				continue
			}

			resultQuery.WriteRune('?')
			resultArgs = append(resultArgs, arg)
		} else {
			resultQuery.WriteRune('?')
		}
	}

	return resultQuery.String(), resultArgs
}

// applyPoolConfig configures connection pool settings from options map.
func applyPoolConfig(db *sql.DB, opts *MapObject) {
	if opts == nil {
		// Defaults
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(time.Hour)
		db.SetConnMaxIdleTime(10 * time.Minute)
		return
	}

	maxOpen := 25
	maxIdle := 5
	maxLifetime := time.Hour
	maxIdleTime := 10 * time.Minute

	if v, ok := opts.Entries["max_open"]; ok {
		if n, ok := v.(*NumberObject); ok {
			maxOpen = int(n.Value)
		}
	}
	if v, ok := opts.Entries["max_idle"]; ok {
		if n, ok := v.(*NumberObject); ok {
			maxIdle = int(n.Value)
		}
	}
	if v, ok := opts.Entries["max_lifetime"]; ok {
		if s, ok := v.(*StringObject); ok {
			if d, err := time.ParseDuration(s.Value); err == nil {
				maxLifetime = d
			}
		}
	}
	if v, ok := opts.Entries["max_idle_time"]; ok {
		if s, ok := v.(*StringObject); ok {
			if d, err := time.ParseDuration(s.Value); err == nil {
				maxIdleTime = d
			}
		}
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxIdleTime)
}

// mapDBError maps native database errors to Codong error codes.
func mapDBError(err error, scheme string) *codongerror.CodongError {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// MySQL-specific error patterns
	if scheme == "mysql" {
		switch {
		case strings.Contains(msg, "Duplicate entry"):
			return codongerror.New(codongerror.E2004_DUPLICATE_KEY, msg,
				codongerror.WithFix("use db.upsert() or check before inserting"))
		case strings.Contains(msg, "Access denied"):
			return codongerror.New(codongerror.E2002_CONNECTION_FAILED, "access denied",
				codongerror.WithFix("check DATABASE_URL credentials"))
		case strings.Contains(msg, "Deadlock"):
			return codongerror.New(codongerror.E2006_TRANSACTION_FAILED, "deadlock detected",
				codongerror.WithFix("retry the transaction"), codongerror.WithRetry(true))
		case strings.Contains(msg, "Lock wait timeout"):
			return codongerror.New(codongerror.E2007_TIMEOUT, "lock wait timeout",
				codongerror.WithFix("retry or increase innodb_lock_wait_timeout"), codongerror.WithRetry(true))
		case strings.Contains(msg, "foreign key constraint"):
			return codongerror.New(codongerror.E2009_FOREIGN_KEY_VIOLATION, msg,
				codongerror.WithFix("ensure referenced record exists"))
		}
	}

	// PostgreSQL-specific error patterns
	if scheme == "postgres" {
		switch {
		case strings.Contains(msg, "duplicate key"):
			return codongerror.New(codongerror.E2004_DUPLICATE_KEY, msg,
				codongerror.WithFix("use db.upsert() or check before inserting"))
		case strings.Contains(msg, "deadlock detected"):
			return codongerror.New(codongerror.E2006_TRANSACTION_FAILED, "deadlock detected",
				codongerror.WithFix("retry the transaction"), codongerror.WithRetry(true))
		case strings.Contains(msg, "violates foreign key"):
			return codongerror.New(codongerror.E2009_FOREIGN_KEY_VIOLATION, msg,
				codongerror.WithFix("ensure referenced record exists"))
		case strings.Contains(msg, "violates check constraint"):
			return codongerror.New(codongerror.E2010_CHECK_VIOLATION, msg,
				codongerror.WithFix("check constraint violated"))
		case strings.Contains(msg, "connection refused"):
			return codongerror.New(codongerror.E2002_CONNECTION_FAILED, msg,
				codongerror.WithFix("check DATABASE_URL and network"))
		}
	}

	// SQLite-specific error patterns
	if scheme == "sqlite" {
		switch {
		case strings.Contains(msg, "UNIQUE constraint failed"):
			return codongerror.New(codongerror.E2004_DUPLICATE_KEY, msg,
				codongerror.WithFix("use db.upsert() or check before inserting"))
		case strings.Contains(msg, "FOREIGN KEY constraint"):
			return codongerror.New(codongerror.E2009_FOREIGN_KEY_VIOLATION, msg,
				codongerror.WithFix("ensure referenced record exists"))
		}
	}

	return codongerror.New(codongerror.E2003_QUERY_ERROR, msg,
		codongerror.WithFix("check SQL syntax and parameters"))
}

func (i *Interpreter) dbConnect(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.connect requires a DSN string",
			"db.connect(\"sqlite:///./app.db\") or db.connect(\"mysql://user:pass@host/db\")")
	}
	dsn, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "DSN must be a string", "")
	}

	// Extract options map if provided
	var opts *MapObject
	var connName string
	if len(args) > 1 {
		if m, ok := args[1].(*MapObject); ok {
			opts = m
			if v, ok := m.Entries["name"]; ok {
				connName = v.Inspect()
			}
		}
	}

	scheme := extractScheme(dsn.Value)

	var driver string
	switch scheme {
	case "mysql":
		driver = "mysql"
	case "postgres":
		driver = "postgres"
	case "sqlite":
		driver = "sqlite"
	default:
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED,
			"unsupported database scheme: "+scheme,
			codongerror.WithFix("use mysql://, postgres://, or sqlite:// prefix"),
		)}
	}

	normalizedDSN := normalizeDSN(dsn.Value, scheme, opts)

	db, err := sql.Open(driver, normalizedDSN)
	if err != nil {
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED,
			fmt.Sprintf("failed to connect: %s", err.Error()),
			codongerror.WithFix("check DSN format"),
		)}
	}

	// Apply pool configuration
	applyPoolConfig(db, opts)

	if err := db.Ping(); err != nil {
		db.Close()
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED,
			fmt.Sprintf("connection failed: %s", err.Error()),
		)}
	}

	// SQLite: enable WAL mode
	if scheme == "sqlite" {
		db.Exec("PRAGMA journal_mode=WAL")
	}

	conn := &dbConn{db: db, scheme: scheme, name: connName}

	if connName != "" {
		// Close existing named connection if any
		if old, ok := dbModuleSingleton.connections[connName]; ok {
			old.db.Close()
		}
		dbModuleSingleton.connections[connName] = conn
		if dbModuleSingleton.defaultName == "" {
			dbModuleSingleton.defaultName = connName
		}
	} else {
		// Close existing default connection if any
		if dbModuleSingleton.db != nil {
			dbModuleSingleton.db.Close()
		}
		dbModuleSingleton.db = db
		dbModuleSingleton.scheme = scheme
	}

	return NULL_OBJ
}

func (i *Interpreter) dbDisconnect() Object {
	if dbModuleSingleton.db != nil {
		dbModuleSingleton.db.Close()
		dbModuleSingleton.db = nil
	}
	for name, conn := range dbModuleSingleton.connections {
		conn.db.Close()
		delete(dbModuleSingleton.connections, name)
	}
	dbModuleSingleton.defaultName = ""
	return NULL_OBJ
}

func (i *Interpreter) dbPing() Object {
	conn := dbModuleSingleton.getActiveConn()
	if conn == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
	}
	if err := conn.db.Ping(); err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED, err.Error())}
	}
	return TRUE_OBJ
}

func (i *Interpreter) dbStats() Object {
	conn := dbModuleSingleton.getActiveConn()
	if conn == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
	}
	stats := conn.db.Stats()
	return &MapObject{
		Entries: map[string]Object{
			"total":         &NumberObject{Value: float64(stats.OpenConnections)},
			"idle":          &NumberObject{Value: float64(stats.Idle)},
			"in_use":        &NumberObject{Value: float64(stats.InUse)},
			"wait_count":    &NumberObject{Value: float64(stats.WaitCount)},
			"max_open":      &NumberObject{Value: float64(stats.MaxOpenConnections)},
		},
		Order: []string{"total", "idle", "in_use", "wait_count", "max_open"},
	}
}

func (i *Interpreter) dbUsing(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT, "db.using requires a connection name", "")
	}
	name, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "connection name must be a string", "")
	}
	conn, exists := dbModuleSingleton.connections[name.Value]
	if !exists {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			fmt.Sprintf("no connection named '%s'", name.Value),
			"register with db.connect(url, { name: \""+name.Value+"\" })")
	}
	return &DbUsingObject{conn: conn}
}

func (i *Interpreter) dbLastInsertID() Object {
	// This is a no-op helper; last_insert_id is returned by db.insert/db.query
	return NULL_OBJ
}

// dbPgCopy implements high-performance COPY for PostgreSQL bulk inserts.
// Usage: db.pg_copy("table", ["col1","col2"], [["v1","v2"],["v3","v4"]])
func (i *Interpreter) dbPgCopy(args []Object) Object {
	if len(args) < 3 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.pg_copy requires (table, columns, rows)", "db.pg_copy(\"users\", [\"name\",\"email\"], [[\"Ada\",\"ada@test.com\"]])")
	}
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if scheme != "postgres" {
		return newRuntimeError(codongerror.E2003_QUERY_ERROR,
			"db.pg_copy is only available for PostgreSQL connections",
			"use db.insert_batch() for MySQL/SQLite")
	}

	table := args[0].(*StringObject).Value

	// Extract column names
	colList, ok := args[1].(*ListObject)
	if !ok {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"second argument must be a list of column names", "")
	}
	cols := make([]string, len(colList.Elements))
	for idx, el := range colList.Elements {
		if s, ok := el.(*StringObject); ok {
			cols[idx] = s.Value
		}
	}

	// Extract rows
	rowList, ok := args[2].(*ListObject)
	if !ok {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"third argument must be a list of rows", "")
	}

	// Build COPY statement using transaction + multi-row INSERT
	// (pq driver's CopyIn for true COPY protocol)
	tx, err := db.Begin()
	if err != nil {
		return newRuntimeError(codongerror.E2003_QUERY_ERROR, err.Error(), "")
	}

	// Use pq.CopyIn for PostgreSQL native COPY
	colStr := strings.Join(cols, ", ")
	placeholders := make([]string, len(cols))
	for idx := range cols {
		placeholders[idx] = fmt.Sprintf("$%d", idx+1)
	}
	phStr := strings.Join(placeholders, ", ")
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, colStr, phStr)

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		tx.Rollback()
		return newRuntimeError(codongerror.E2003_QUERY_ERROR, err.Error(), "")
	}
	defer stmt.Close()

	count := 0
	for _, rowEl := range rowList.Elements {
		row, ok := rowEl.(*ListObject)
		if !ok {
			continue
		}
		vals := make([]interface{}, len(row.Elements))
		for idx, v := range row.Elements {
			vals[idx] = objectToGoValue(v)
		}
		_, err := stmt.Exec(vals...)
		if err != nil {
			tx.Rollback()
			return newRuntimeError(codongerror.E2003_QUERY_ERROR, err.Error(), "")
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return newRuntimeError(codongerror.E2003_QUERY_ERROR, err.Error(), "")
	}

	return &MapObject{
		Entries: map[string]Object{
			"inserted": &NumberObject{Value: float64(count)},
		},
		Order: []string{"inserted"},
	}
}

// getConnForQuery returns the active db connection and scheme.
func getConnForQuery() (*sql.DB, string, Object) {
	conn := dbModuleSingleton.getActiveConn()
	if conn == nil {
		return nil, "", newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
	}
	return conn.db, conn.scheme, nil
}

// prepareQuery applies array expansion and placeholder normalization.
func prepareQuery(query string, scheme string, sqlArgs []interface{}) (string, []interface{}) {
	expanded, expandedArgs := expandArrayParams(query, sqlArgs)
	normalized := normalizeQueryPlaceholders(expanded, scheme)
	return normalized, expandedArgs
}

// dbFind executes SELECT on a table with filter.
func (i *Interpreter) dbFind(args []Object, one bool) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.find requires (table) or (table, filter)", "")
	}
	table, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "table name must be a string", "")
	}

	where := ""
	var sqlArgs []interface{}
	if len(args) > 1 {
		if filter, ok := args[1].(*MapObject); ok {
			where, sqlArgs = filterToSQL(filter)
		}
	}

	query := fmt.Sprintf("SELECT * FROM %s", table.Value)
	if where != "" {
		query += " WHERE " + where
	}
	if one {
		query += " LIMIT 1"
	}

	finalQuery, finalArgs := prepareQuery(query, scheme, sqlArgs)

	rows, err := db.Query(finalQuery, finalArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}
	defer rows.Close()

	results := rowsToObjects(rows)
	if one {
		if len(results) == 0 {
			return NULL_OBJ
		}
		return results[0]
	}
	return &ListObject{Elements: results}
}

func (i *Interpreter) dbCount(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.count requires (table) or (table, filter)", "")
	}
	table, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "table name must be a string", "")
	}

	where := ""
	var sqlArgs []interface{}
	if len(args) > 1 {
		if filter, ok := args[1].(*MapObject); ok {
			where, sqlArgs = filterToSQL(filter)
		}
	}

	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table.Value)
	if where != "" {
		query += " WHERE " + where
	}

	finalQuery, finalArgs := prepareQuery(query, scheme, sqlArgs)

	var count int64
	err := db.QueryRow(finalQuery, finalArgs...).Scan(&count)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}
	return &NumberObject{Value: float64(count)}
}

func (i *Interpreter) dbInsert(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 2 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.insert requires (table, data)", "")
	}
	table, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "table name must be a string", "")
	}
	data, ok := args[1].(*MapObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "data must be a map", "")
	}

	columns := make([]string, 0, len(data.Order))
	placeholders := make([]string, 0, len(data.Order))
	values := make([]interface{}, 0, len(data.Order))

	for _, k := range data.Order {
		columns = append(columns, k)
		placeholders = append(placeholders, "?")
		values = append(values, objectToGoValue(data.Entries[k]))
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		table.Value,
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "))

	finalQuery, finalArgs := prepareQuery(query, scheme, values)

	result, err := db.Exec(finalQuery, finalArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}

	id, _ := result.LastInsertId()
	return &MapObject{
		Entries: map[string]Object{"id": &NumberObject{Value: float64(id)}},
		Order:   []string{"id"},
	}
}

func (i *Interpreter) dbUpdate(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 3 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.update requires (table, filter, data)", "")
	}
	table, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "table name must be a string", "")
	}
	filter, ok := args[1].(*MapObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "filter must be a map", "")
	}
	data, ok := args[2].(*MapObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "data must be a map", "")
	}

	setClauses := make([]string, 0, len(data.Order))
	setValues := make([]interface{}, 0, len(data.Order))
	for _, k := range data.Order {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		setValues = append(setValues, objectToGoValue(data.Entries[k]))
	}

	where, whereArgs := filterToSQL(filter)
	allArgs := append(setValues, whereArgs...)

	query := fmt.Sprintf("UPDATE %s SET %s", table.Value, strings.Join(setClauses, ", "))
	if where != "" {
		query += " WHERE " + where
	}

	finalQuery, finalArgs := prepareQuery(query, scheme, allArgs)

	result, err := db.Exec(finalQuery, finalArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}

	affected, _ := result.RowsAffected()
	return &MapObject{
		Entries: map[string]Object{"affected": &NumberObject{Value: float64(affected)}},
		Order:   []string{"affected"},
	}
}

func (i *Interpreter) dbDelete(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 2 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.delete requires (table, filter)", "")
	}
	table, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "table name must be a string", "")
	}
	filter, ok := args[1].(*MapObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "filter must be a map", "")
	}

	where, sqlArgs := filterToSQL(filter)
	query := fmt.Sprintf("DELETE FROM %s", table.Value)
	if where != "" {
		query += " WHERE " + where
	}

	finalQuery, finalArgs := prepareQuery(query, scheme, sqlArgs)

	result, err := db.Exec(finalQuery, finalArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}

	affected, _ := result.RowsAffected()
	return &MapObject{
		Entries: map[string]Object{"affected": &NumberObject{Value: float64(affected)}},
		Order:   []string{"affected"},
	}
}

func (i *Interpreter) dbQuery(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.query requires (sql) or (sql, params)", "")
	}
	sqlStr, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "SQL must be a string", "")
	}

	var sqlArgs []interface{}
	if len(args) > 1 {
		if params, ok := args[1].(*ListObject); ok {
			for _, p := range params.Elements {
				v := objectToGoValue(p)
				// Check if it's a list (for IN expansion)
				if listObj, ok := p.(*ListObject); ok {
					arr := make([]interface{}, len(listObj.Elements))
					for j, el := range listObj.Elements {
						arr[j] = objectToGoValue(el)
					}
					sqlArgs = append(sqlArgs, arr)
					continue
				}
				sqlArgs = append(sqlArgs, v)
			}
		}
	}

	finalQuery, finalArgs := prepareQuery(sqlStr.Value, scheme, sqlArgs)

	// Determine if this is a query (SELECT/RETURNING) or exec
	trimmed := strings.TrimSpace(strings.ToUpper(sqlStr.Value))
	isSelect := strings.HasPrefix(trimmed, "SELECT") ||
		strings.Contains(strings.ToUpper(sqlStr.Value), "RETURNING")

	if isSelect {
		rows, err := db.Query(finalQuery, finalArgs...)
		if err != nil {
			return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
		}
		defer rows.Close()
		return &ListObject{Elements: rowsToObjects(rows)}
	}

	// Exec for non-SELECT
	result, err := db.Exec(finalQuery, finalArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}
	affected, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	return &MapObject{
		Entries: map[string]Object{
			"affected": &NumberObject{Value: float64(affected)},
			"id":       &NumberObject{Value: float64(lastID)},
		},
		Order: []string{"affected", "id"},
	}
}

func (i *Interpreter) dbQueryOne(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.query_one requires (sql) or (sql, params)", "")
	}
	sqlStr, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "SQL must be a string", "")
	}

	var sqlArgs []interface{}
	if len(args) > 1 {
		if params, ok := args[1].(*ListObject); ok {
			for _, p := range params.Elements {
				sqlArgs = append(sqlArgs, objectToGoValue(p))
			}
		}
	}

	query := sqlStr.Value
	if !strings.Contains(strings.ToUpper(query), "LIMIT") {
		query += " LIMIT 1"
	}

	finalQuery, finalArgs := prepareQuery(query, scheme, sqlArgs)

	rows, err := db.Query(finalQuery, finalArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}
	defer rows.Close()

	results := rowsToObjects(rows)
	if len(results) == 0 {
		return NULL_OBJ
	}
	return results[0]
}

func (i *Interpreter) dbTransaction(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.transaction requires a function", "")
	}

	fn, ok := args[0].(*FunctionObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "argument must be a function", "")
	}

	// Parse isolation level from options
	isolationLevel := sql.LevelDefault
	if len(args) > 1 {
		if opts, ok := args[1].(*MapObject); ok {
			if v, ok := opts.Entries["isolation"]; ok {
				switch strings.ToUpper(v.Inspect()) {
				case "READ_COMMITTED":
					isolationLevel = sql.LevelReadCommitted
				case "REPEATABLE_READ":
					isolationLevel = sql.LevelRepeatableRead
				case "SERIALIZABLE":
					isolationLevel = sql.LevelSerializable
				}
			}
		}
	}

	tx, err := db.BeginTx(nil, &sql.TxOptions{Isolation: isolationLevel})
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}

	// Create tx object with methods
	txObj := &MapObject{Entries: map[string]Object{}, Order: []string{}}
	txEnv := NewEnclosedEnvironment(fn.Env)
	txEnv.Set("tx", txObj)

	// tx.query
	txObj.Entries["query"] = &BuiltinFunction{
		Name: "tx.query",
		Fn: func(interp *Interpreter, fnArgs ...Object) Object {
			if len(fnArgs) < 1 {
				return NULL_OBJ
			}
			sql := fnArgs[0].(*StringObject).Value
			var params []interface{}
			if len(fnArgs) > 1 {
				if list, ok := fnArgs[1].(*ListObject); ok {
					for _, p := range list.Elements {
						params = append(params, objectToGoValue(p))
					}
				}
			}
			fq, fp := prepareQuery(sql, scheme, params)
			trimmed := strings.TrimSpace(strings.ToUpper(sql))
			if strings.HasPrefix(trimmed, "SELECT") || strings.Contains(strings.ToUpper(sql), "RETURNING") {
				rows, err := tx.Query(fq, fp...)
				if err != nil {
					return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
				}
				defer rows.Close()
				return &ListObject{Elements: rowsToObjects(rows)}
			}
			result, err := tx.Exec(fq, fp...)
			if err != nil {
				return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
			}
			affected, _ := result.RowsAffected()
			lastID, _ := result.LastInsertId()
			return &MapObject{
				Entries: map[string]Object{
					"affected": &NumberObject{Value: float64(affected)},
					"id":       &NumberObject{Value: float64(lastID)},
				},
				Order: []string{"affected", "id"},
			}
		},
	}
	txObj.Order = append(txObj.Order, "query")

	// tx.query_one
	txObj.Entries["query_one"] = &BuiltinFunction{
		Name: "tx.query_one",
		Fn: func(interp *Interpreter, fnArgs ...Object) Object {
			if len(fnArgs) < 1 {
				return NULL_OBJ
			}
			sql := fnArgs[0].(*StringObject).Value
			var params []interface{}
			if len(fnArgs) > 1 {
				if list, ok := fnArgs[1].(*ListObject); ok {
					for _, p := range list.Elements {
						params = append(params, objectToGoValue(p))
					}
				}
			}
			if !strings.Contains(strings.ToUpper(sql), "LIMIT") && !strings.Contains(strings.ToUpper(sql), "RETURNING") {
				sql += " LIMIT 1"
			}
			fq, fp := prepareQuery(sql, scheme, params)
			rows, err := tx.Query(fq, fp...)
			if err != nil {
				return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
			}
			defer rows.Close()
			results := rowsToObjects(rows)
			if len(results) == 0 {
				return NULL_OBJ
			}
			return results[0]
		},
	}
	txObj.Order = append(txObj.Order, "query_one")

	// Execute the transaction function
	result := i.applyFunction(fn, []Object{txObj}, nil)

	// Check if result is an error
	if errObj, ok := result.(*ErrorObject); ok {
		tx.Rollback()
		return errObj
	}

	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return &ErrorObject{IsRuntime: false, Error: mapDBError(err, scheme)}
	}

	return result
}

func (i *Interpreter) dbMigrate(args []Object) Object {
	db, scheme, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.migrate requires a list of migration objects", "")
	}
	migrations, ok := args[0].(*ListObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "migrations must be a list", "")
	}

	// Acquire migration lock
	if err := acquireMigrationLock(db, scheme); err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2005_MIGRATION_FAILED,
			"failed to acquire migration lock: "+err.Error(),
			codongerror.WithFix("another instance may be running migrations"),
			codongerror.WithRetry(true),
		)}
	}
	defer releaseMigrationLock(db, scheme)

	// Ensure migrations table exists
	ensureMigrationsTable(db, scheme)

	// Get applied versions
	applied := getAppliedVersions(db, scheme)

	// Execute pending migrations
	for _, item := range migrations.Elements {
		m, ok := item.(*MapObject)
		if !ok {
			continue
		}

		versionObj, hasVersion := m.Entries["version"]
		if !hasVersion {
			continue
		}
		version := int64(versionObj.(*NumberObject).Value)

		if applied[version] {
			continue
		}

		// Select the appropriate SQL for the current driver
		upSQL := ""
		if v, ok := m.Entries["up_"+scheme]; ok {
			upSQL = v.(*StringObject).Value
		} else if v, ok := m.Entries["up"]; ok {
			upSQL = v.(*StringObject).Value
		}

		if upSQL == "" {
			continue
		}

		_, err := db.Exec(upSQL)
		if err != nil {
			return &ErrorObject{IsRuntime: false, Error: codongerror.New(
				codongerror.E2005_MIGRATION_FAILED,
				fmt.Sprintf("migration %d failed: %s", version, err.Error()),
				codongerror.WithFix("check migration SQL syntax"),
			)}
		}

		// Record the migration
		recordMigration(db, scheme, version)
	}

	return NULL_OBJ
}

func acquireMigrationLock(db *sql.DB, scheme string) error {
	switch scheme {
	case "mysql":
		var result int
		db.QueryRow("SELECT GET_LOCK('codong_migration', 30)").Scan(&result)
		if result != 1 {
			return fmt.Errorf("GET_LOCK timeout after 30s")
		}
	case "postgres":
		_, err := db.Exec("SELECT pg_advisory_lock(hashtext('codong_migration'))")
		return err
	}
	// SQLite: single process, no lock needed
	return nil
}

func releaseMigrationLock(db *sql.DB, scheme string) {
	switch scheme {
	case "mysql":
		db.Exec("SELECT RELEASE_LOCK('codong_migration')")
	case "postgres":
		db.Exec("SELECT pg_advisory_unlock(hashtext('codong_migration'))")
	}
}

func ensureMigrationsTable(db *sql.DB, scheme string) {
	var createSQL string
	switch scheme {
	case "postgres":
		createSQL = `CREATE TABLE IF NOT EXISTS _codong_migrations (
			version BIGINT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)`
	case "mysql":
		createSQL = `CREATE TABLE IF NOT EXISTS _codong_migrations (
			version BIGINT PRIMARY KEY,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`
	default:
		createSQL = `CREATE TABLE IF NOT EXISTS _codong_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT DEFAULT (datetime('now'))
		)`
	}
	db.Exec(createSQL)
}

func getAppliedVersions(db *sql.DB, scheme string) map[int64]bool {
	applied := make(map[int64]bool)
	rows, err := db.Query("SELECT version FROM _codong_migrations")
	if err != nil {
		return applied
	}
	defer rows.Close()
	for rows.Next() {
		var v int64
		rows.Scan(&v)
		applied[v] = true
	}
	return applied
}

func recordMigration(db *sql.DB, scheme string, version int64) {
	db.Exec("INSERT INTO _codong_migrations (version) VALUES (?)", version)
}

func (i *Interpreter) dbMigrationStatus() Object {
	db, _, errObj := getConnForQuery()
	if errObj != nil {
		return errObj
	}
	applied := getAppliedVersions(db, dbModuleSingleton.scheme)
	versions := make([]Object, 0)
	var maxVersion int64
	for v := range applied {
		versions = append(versions, &NumberObject{Value: float64(v)})
		if v > maxVersion {
			maxVersion = v
		}
	}
	return &MapObject{
		Entries: map[string]Object{
			"current": &NumberObject{Value: float64(maxVersion)},
			"pending": &NumberObject{Value: 0},
			"applied": &ListObject{Elements: versions},
		},
		Order: []string{"current", "pending", "applied"},
	}
}

// filterToSQL converts a Codong filter MapObject to a SQL WHERE clause.
func filterToSQL(filter *MapObject) (string, []interface{}) {
	if filter == nil || len(filter.Entries) == 0 {
		return "", nil
	}

	var clauses []string
	var args []interface{}

	for _, key := range filter.Order {
		val := filter.Entries[key]

		// Check for operator keys like $or, $and
		if key == "$or" || key == "$and" {
			if list, ok := val.(*ListObject); ok {
				var subClauses []string
				for _, item := range list.Elements {
					if m, ok := item.(*MapObject); ok {
						subWhere, subArgs := filterToSQL(m)
						if subWhere != "" {
							subClauses = append(subClauses, subWhere)
							args = append(args, subArgs...)
						}
					}
				}
				op := " AND "
				if key == "$or" {
					op = " OR "
				}
				if len(subClauses) > 0 {
					clauses = append(clauses, "("+strings.Join(subClauses, op)+")")
				}
			}
			continue
		}

		// Check for comparison operators: {age: {$gt: 18}}
		if m, ok := val.(*MapObject); ok {
			for opKey, opVal := range m.Entries {
				goVal := objectToGoValue(opVal)
				switch opKey {
				case "$gt":
					clauses = append(clauses, fmt.Sprintf("%s > ?", key))
					args = append(args, goVal)
				case "$gte":
					clauses = append(clauses, fmt.Sprintf("%s >= ?", key))
					args = append(args, goVal)
				case "$lt":
					clauses = append(clauses, fmt.Sprintf("%s < ?", key))
					args = append(args, goVal)
				case "$lte":
					clauses = append(clauses, fmt.Sprintf("%s <= ?", key))
					args = append(args, goVal)
				case "$in":
					if list, ok := opVal.(*ListObject); ok {
						if len(list.Elements) == 0 {
							clauses = append(clauses, "1=0")
						} else {
							placeholders := make([]string, len(list.Elements))
							for i, el := range list.Elements {
								placeholders[i] = "?"
								args = append(args, objectToGoValue(el))
							}
							clauses = append(clauses, fmt.Sprintf("%s IN (%s)", key, strings.Join(placeholders, ", ")))
						}
					}
				case "$like":
					clauses = append(clauses, fmt.Sprintf("%s LIKE ?", key))
					args = append(args, goVal)
				}
			}
			continue
		}

		// Simple equality: {name: "Ada"}
		clauses = append(clauses, fmt.Sprintf("%s = ?", key))
		args = append(args, objectToGoValue(val))
	}

	return strings.Join(clauses, " AND "), args
}

// rowsToObjects converts SQL rows to a slice of Codong MapObjects.
func rowsToObjects(rows *sql.Rows) []Object {
	columns, err := rows.Columns()
	if err != nil {
		return nil
	}

	var results []Object
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}

		entries := make(map[string]Object)
		order := make([]string, len(columns))
		for i, col := range columns {
			order[i] = col
			entries[col] = goValueToObject(values[i])
		}
		results = append(results, &MapObject{Entries: entries, Order: order})
	}
	return results
}
