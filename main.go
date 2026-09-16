package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mssql-batch-compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("out", "", "also write the UTF-8 report to this file (overwrites on success)")
	timeout := flags.Duration("timeout", 60*time.Second, "total connection and schema-read timeout, e.g. 30s or 2m")
	modeFlag := flags.String("sql-mode", string(sqlModeStrict), "SQL definition comparison: strict or normalized")
	format := flags.String("format", "text", "report format: text or html (self-contained, offline)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: mssql-batch-compare [-out diff.txt] [-format text|html] [-timeout 60s] [-sql-mode strict|normalized]")
		fmt.Fprintln(stderr, "\nRequired settings (environment variables or .env in the current working directory):")
		fmt.Fprintln(stderr, "  MSSQL_SOURCE_DSN       source SQL Server connection string")
		fmt.Fprintln(stderr, "  MSSQL_DESTINATION_DSN  destination SQL Server connection string")
		fmt.Fprintln(stderr, "  MSSQL_GENERATE_MIGRATION  true to generate SQL, false by default")
		fmt.Fprintln(stderr, "  MSSQL_MIGRATION_OUT       SQL output path, default migration.sql")
		fmt.Fprintln(stderr, "Migration covers SQL procedures/views/functions and synonyms only; it never runs the script.")
		fmt.Fprintln(stderr, "Changed/missing CLR functions require manual assembly/function migration; equal CLR prerequisites are allowed.")
		fmt.Fprintln(stderr, "Ordering follows catalog dependencies, including synonym targets. Unresolved/external references,")
		fmt.Fprintln(stderr, "cycles, unsafe changes and schema-bound blockers fail generation; dynamic SQL requires manual review.")
		fmt.Fprintln(stderr, "Migration also requires SELECT on sys.sql_expression_dependencies on both databases.")
		fmt.Fprintln(stderr, "Run generated SQL in the exact destination database/server; it uses a guarded transaction.")
		fmt.Fprintln(stderr, "Existing environment variables take precedence over .env, including explicitly empty values.")
		fmt.Fprintln(stderr, "A missing .env is allowed; an unreadable or invalid .env is an error.")
		fmt.Fprintln(stderr, "Use UTF-8 and single-quote complete DSNs in .env to preserve $, # and backslashes literally.")
		fmt.Fprintln(stderr, "See .env.example. Report format, SQL mode, timeout and output path are still CLI options.")
		fmt.Fprintln(stderr, "Both strings must explicitly specify database. Credentials are never included in the report.")
		fmt.Fprintln(stderr, "Use encrypt=true;TrustServerCertificate=false with a trusted server certificate.")
		fmt.Fprintln(stderr, "Requires database-level VIEW DEFINITION on both databases; object-level DENY can hide metadata.")
		fmt.Fprintln(stderr, "Read-only comparison: table/column names, data types, NULL, and primary keys.")
		fmt.Fprintln(stderr, "Also compares SQL stored procedures, views, functions (scalar/inline/multi-statement TVF), and synonyms.")
		fmt.Fprintln(stderr, "SQL modules: definition plus ANSI_NULLS/QUOTED_IDENTIFIER. Synonyms: referenced target name only.")
		fmt.Fprintln(stderr, "CLR scalar/table-valued functions (FS/FT): assembly identity/permission set/main-DLL SHA-256, class/method,")
		fmt.Fprintln(stderr, "ordered parameter/default/return signatures, EXECUTE AS and NULL ON NULL INPUT; SQL modes do not apply.")
		fmt.Fprintln(stderr, "CLR dependency assemblies, ancillary files and instance-level CLR settings are not compared.")
		fmt.Fprintln(stderr, "Both modes normalize CRLF to LF. Strict compares all remaining definition text.")
		fmt.Fprintln(stderr, "Normalized additionally unifies CREATE/ALTER/CREATE OR ALTER and spacing before the module type.")
		fmt.Fprintln(stderr, "Comments, literals, identifiers and the rest of the SQL remain significant; this is not semantic comparison.")
		fmt.Fprintln(stderr, "SQL changes use unified diffs with 3 context lines and original-definition line numbers.")
		fmt.Fprintln(stderr, "In normalized mode, ignored declaration changes may appear in hunks when another difference exists.")
		fmt.Fprintln(stderr, "Encrypted/unreadable SQL or CLR metadata and unsupported PC/AF/X modules cause an error; never silently skipped.")
		fmt.Fprintln(stderr, "Reports contain SQL definitions; protect the output if your SQL embeds sensitive values.")
		fmt.Fprintln(stderr, "Use -format html -out diff.html for a standalone browser report; text is the default.")
		fmt.Fprintln(stderr, "The selected format is written to stdout and, when specified, to -out. The filename does not select the format.")
		fmt.Fprintln(stderr, "Names are case-sensitive; PK constraint names and column ordinal positions are ignored.")
		fmt.Fprintln(stderr, "Does not compare row data or table column order/defaults/identity/computed expressions/collation/non-PK indexes,")
		fmt.Fprintln(stderr, "FK/CHECK/UNIQUE constraints, triggers, object GRANT/DENY permissions, or type/XML schema definitions.")
		fmt.Fprintln(stderr, "Avoid concurrent DDL; the two databases are read sequentially, not in a shared snapshot.")
		fmt.Fprintln(stderr, "Exit codes: 0 = no differences in scope, 1 = differences found, 2 = error.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	if flags.NArg() != 0 {
		return fail(fmt.Errorf("unexpected positional arguments; use -help for usage"))
	}
	if *timeout <= 0 {
		return fail(fmt.Errorf("-timeout must be greater than zero"))
	}
	mode := sqlCompareMode(*modeFlag)
	if mode != sqlModeStrict && mode != sqlModeNormalized {
		return fail(fmt.Errorf("-sql-mode must be strict or normalized"))
	}
	if *format != "text" && *format != "html" {
		return fail(fmt.Errorf("-format must be text or html"))
	}
	config, err := loadConfig()
	if err != nil {
		return fail(err)
	}
	if strings.TrimSpace(config.sourceDSN) == "" || strings.TrimSpace(config.destinationDSN) == "" {
		return fail(fmt.Errorf("set both MSSQL_SOURCE_DSN and MSSQL_DESTINATION_DSN in the environment or .env"))
	}
	if config.generateMigration {
		if err := validateMigrationOutput(config.migrationOut, *output); err != nil {
			return fail(err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	source, err := loadSchema(ctx, config.sourceDSN, config.generateMigration)
	if err != nil {
		return fail(fmt.Errorf("source: %w", err))
	}
	destination, err := loadSchema(ctx, config.destinationDSN, config.generateMigration)
	if err != nil {
		return fail(fmt.Errorf("destination: %w", err))
	}
	diffs := compareSchemas(source, destination, mode)
	var migrationSQL string
	var migrationSteps []migrationStep
	if config.generateMigration {
		var notes []string
		migrationSteps, notes, err = planMigration(source, destination, mode)
		if err != nil {
			return fail(fmt.Errorf("plan migration: %w", err))
		}
		migrationSQL, err = renderMigrationSQL(destination, migrationSteps, notes)
		if err != nil {
			return fail(fmt.Errorf("render migration: %w", err))
		}
	}
	var report string
	if *format == "html" {
		report, err = renderHTMLReport(source, destination, diffs, mode)
		if err != nil {
			return fail(fmt.Errorf("render HTML report: %w", err))
		}
	} else {
		report = renderReport(source, destination, diffs, mode)
	}
	if *output != "" {
		if err := os.WriteFile(*output, []byte(report), 0600); err != nil {
			return fail(fmt.Errorf("write report: %w", err))
		}
	}
	if config.generateMigration {
		if err := writeMigrationFile(config.migrationOut, migrationSQL); err != nil {
			return fail(fmt.Errorf("write migration: %w", err))
		}
		fmt.Fprintf(stderr, "Migration script: %s (%d operation(s)); review before running on destination.\n", config.migrationOut, len(migrationSteps))
	}
	if _, err := io.WriteString(stdout, report); err != nil {
		return fail(fmt.Errorf("write stdout: %w", err))
	}
	if len(diffs) != 0 {
		return 1
	}
	return 0
}
