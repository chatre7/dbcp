package main

// Dependencies point from a referencing object to the object it needs.
// An issue means the reference cannot be validated within this database.
type migrationDependency struct {
	name        objectName
	schemaBound bool
	issue       string
}

type migrationMetadata struct {
	databaseName  string
	serverName    string
	schemas       map[string]bool
	objectTypes   map[objectName]string
	dependencies  map[objectName][]migrationDependency
	unsafeObjects map[objectName]string
}

type migrationStep struct {
	name   objectName
	object schemaObject
	// CREATE or ALTER for SQL modules; CREATE or REPLACE for synonyms.
	action string
}
