package interpreter

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/codong-lang/codong/stdlib/codongerror"

	_ "modernc.org/sqlite"
)

// DbModuleObject is the singleton `db` module.
type DbModuleObject struct {
	db *sql.DB
}

func (d *DbModuleObject) Type() string    { return "module" }
func (d *DbModuleObject) Inspect() string { return "<module:db>" }

var dbModuleSingleton = &DbModuleObject{}

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
			case "ping":
				return i.dbPing()
			default:
				return newRuntimeError(codongerror.E2003_QUERY_ERROR,
					fmt.Sprintf("unknown db method: %s", method), "")
			}
		},
	}
}

func (i *Interpreter) dbConnect(args []Object) Object {
	if len(args) < 1 {
		return newRuntimeError(codongerror.E1005_INVALID_ARGUMENT,
			"db.connect requires a DSN string",
			"db.connect(\"file:mydb.sqlite\")")
	}
	dsn, ok := args[0].(*StringObject)
	if !ok {
		return newRuntimeError(codongerror.E1002_TYPE_MISMATCH, "DSN must be a string", "")
	}

	// Close existing connection if any
	if dbModuleSingleton.db != nil {
		dbModuleSingleton.db.Close()
	}

	db, err := sql.Open("sqlite", dsn.Value)
	if err != nil {
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED,
			fmt.Sprintf("failed to connect: %s", err.Error()),
			codongerror.WithFix("check DSN format: db.connect(\"file:mydb.sqlite\")"),
		)}
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return &ErrorObject{IsRuntime: true, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED,
			fmt.Sprintf("connection failed: %s", err.Error()),
		)}
	}

	// Enable WAL mode for better concurrent performance
	db.Exec("PRAGMA journal_mode=WAL")

	dbModuleSingleton.db = db
	return NULL_OBJ
}

func (i *Interpreter) dbDisconnect() Object {
	if dbModuleSingleton.db != nil {
		dbModuleSingleton.db.Close()
		dbModuleSingleton.db = nil
	}
	return NULL_OBJ
}

func (i *Interpreter) dbPing() Object {
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
	}
	if err := dbModuleSingleton.db.Ping(); err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2002_CONNECTION_FAILED, err.Error())}
	}
	return TRUE_OBJ
}

// dbFind executes SELECT on a table with filter.
func (i *Interpreter) dbFind(args []Object, one bool) Object {
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
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

	rows, err := dbModuleSingleton.db.Query(query, sqlArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2003_QUERY_ERROR, err.Error())}
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
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
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

	var count int64
	err := dbModuleSingleton.db.QueryRow(query, sqlArgs...).Scan(&count)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2003_QUERY_ERROR, err.Error())}
	}
	return &NumberObject{Value: float64(count)}
}

func (i *Interpreter) dbInsert(args []Object) Object {
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
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

	result, err := dbModuleSingleton.db.Exec(query, values...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2003_QUERY_ERROR, err.Error())}
	}

	id, _ := result.LastInsertId()
	return &MapObject{
		Entries: map[string]Object{"id": &NumberObject{Value: float64(id)}},
		Order:   []string{"id"},
	}
}

func (i *Interpreter) dbUpdate(args []Object) Object {
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
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

	result, err := dbModuleSingleton.db.Exec(query, allArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2003_QUERY_ERROR, err.Error())}
	}

	affected, _ := result.RowsAffected()
	return &MapObject{
		Entries: map[string]Object{"affected": &NumberObject{Value: float64(affected)}},
		Order:   []string{"affected"},
	}
}

func (i *Interpreter) dbDelete(args []Object) Object {
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
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

	result, err := dbModuleSingleton.db.Exec(query, sqlArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2003_QUERY_ERROR, err.Error())}
	}

	affected, _ := result.RowsAffected()
	return &MapObject{
		Entries: map[string]Object{"affected": &NumberObject{Value: float64(affected)}},
		Order:   []string{"affected"},
	}
}

func (i *Interpreter) dbQuery(args []Object) Object {
	if dbModuleSingleton.db == nil {
		return newRuntimeError(codongerror.E2002_CONNECTION_FAILED,
			"no database connection", "call db.connect() first")
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
				sqlArgs = append(sqlArgs, objectToGoValue(p))
			}
		}
	}

	// Determine if this is a query (SELECT) or exec (INSERT/UPDATE/DELETE/CREATE)
	trimmed := strings.TrimSpace(strings.ToUpper(sqlStr.Value))
	if strings.HasPrefix(trimmed, "SELECT") {
		rows, err := dbModuleSingleton.db.Query(sqlStr.Value, sqlArgs...)
		if err != nil {
			return &ErrorObject{IsRuntime: false, Error: codongerror.New(
				codongerror.E2003_QUERY_ERROR, err.Error())}
		}
		defer rows.Close()
		return &ListObject{Elements: rowsToObjects(rows)}
	}

	// Exec for non-SELECT
	result, err := dbModuleSingleton.db.Exec(sqlStr.Value, sqlArgs...)
	if err != nil {
		return &ErrorObject{IsRuntime: false, Error: codongerror.New(
			codongerror.E2003_QUERY_ERROR, err.Error())}
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
						placeholders := make([]string, len(list.Elements))
						for i, el := range list.Elements {
							placeholders[i] = "?"
							args = append(args, objectToGoValue(el))
						}
						clauses = append(clauses, fmt.Sprintf("%s IN (%s)", key, strings.Join(placeholders, ", ")))
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
