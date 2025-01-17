/*
Copyright (c) YugabyteDB, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package dbzm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ColumnSchema struct {
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Parameters map[string]string `json:"parameters"`
	// Not decoding the rest of the fields for now.
}

type Column struct {
	Name   string       `json:"name"`
	Index  int64        `json:"index"`
	Schema ColumnSchema `json:"schema"`
}

type TableSchema struct {
	Columns []Column `json:"columns"`
}

func (ts *TableSchema) getColumnType(columnName string, getSourceDatatypeIfRequired bool) (string, error) {
	for _, colSchema := range ts.Columns {
		if colSchema.Name == columnName { //TODO: store the column names in map for faster lookup
			if dbColType, ok := colSchema.Schema.Parameters["__debezium.source.column.type"]; ok && getSourceDatatypeIfRequired &&
				(strings.Contains(dbColType, "DATE") || strings.Contains(dbColType, "INTERVAL")) {
				//in case oracle import for DATE and INTERVAL types, need to use the original type from source
				return dbColType, nil
			} else if colSchema.Schema.Name != "" { // in case Kafka speicfic type which start with io.debezium...
				return colSchema.Schema.Name, nil
			} else {
				return colSchema.Schema.Type, nil // in case of Primitive types e.g. BYTES/STRING..
			}

		}
	}
	return "", fmt.Errorf("Column %s not found in table schema %v", columnName, ts)
}

//===========================================================

type SchemaRegistry struct {
	exportDir         string
	exporterRole      string
	tableNameToSchema map[string]*TableSchema
}

func NewSchemaRegistry(exportDir string, exporterRole string) *SchemaRegistry {
	return &SchemaRegistry{
		exportDir:         exportDir,
		exporterRole:      exporterRole,
		tableNameToSchema: make(map[string]*TableSchema),
	}
}

func (sreg *SchemaRegistry) GetColumnTypes(tableName string, columnNames []string, getSourceDatatypes bool) ([]string, error) {
	tableSchema := sreg.tableNameToSchema[tableName]
	if tableSchema == nil {
		return nil, fmt.Errorf("table %s not found in schema registry", tableName)
	}
	columnTypes := make([]string, len(columnNames))
	for idx, columnName := range columnNames {
		var err error
		columnTypes[idx], err = tableSchema.getColumnType(columnName, getSourceDatatypes)
		if err != nil {
			return nil, fmt.Errorf("failed to get column type for table %s, column %s: %w", tableName, columnName, err)
		}
	}
	return columnTypes, nil
}

func (sreg *SchemaRegistry) GetColumnType(tableName, columnName string, getSourceDatatype bool) (string, error) {
	var tableSchema *TableSchema
	var err error
	tableSchema = sreg.tableNameToSchema[tableName]
	if tableSchema == nil {
		// check on disk
		tableSchema, err = sreg.getAndStoreTableSchema(tableName)
		if err != nil {
			return "", fmt.Errorf("table %s not found in schema registry:%w", tableName, err)
		}
	}
	return tableSchema.getColumnType(columnName, getSourceDatatype)
}

func (sreg *SchemaRegistry) Init() error {
	schemaDir := filepath.Join(sreg.exportDir, "data", "schemas", sreg.exporterRole)
	schemaFiles, err := os.ReadDir(schemaDir)
	if err != nil {
		return fmt.Errorf("failed to read schema dir %s: %w", schemaDir, err)
	}
	for _, schemaFile := range schemaFiles {
		schemaFilePath := filepath.Join(schemaDir, schemaFile.Name())
		schemaFile, err := os.Open(schemaFilePath)
		if err != nil {
			return fmt.Errorf("failed to open table schema file %s: %w", schemaFilePath, err)
		}
		var tableSchema TableSchema
		err = json.NewDecoder(schemaFile).Decode(&tableSchema)
		if err != nil {
			return fmt.Errorf("failed to decode table schema file %s: %w", schemaFilePath, err)
		}
		table := strings.TrimSuffix(filepath.Base(schemaFile.Name()), "_schema.json")
		sreg.tableNameToSchema[table] = &tableSchema
		schemaFile.Close()
	}
	return nil
}

func (sreg *SchemaRegistry) getAndStoreTableSchema(tableName string) (*TableSchema, error) {
	schemaFilePath := filepath.Join(sreg.exportDir, "data", "schemas", sreg.exporterRole, fmt.Sprintf("%s_schema.json", tableName))
	schemaFile, err := os.Open(schemaFilePath)
	defer func() {
		_ = schemaFile.Close()
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to open table schema file %s: %w", schemaFilePath, err)
	}
	var tableSchema TableSchema
	err = json.NewDecoder(schemaFile).Decode(&tableSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to decode table schema file %s: %w", schemaFilePath, err)
	}
	sreg.tableNameToSchema[tableName] = &tableSchema
	return &tableSchema, nil
}
