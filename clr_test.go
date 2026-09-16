package main

import (
	"strings"
	"testing"
)

func TestCLRSignatureDistinguishesAbsentAndNullDefaults(t *testing.T) {
	parameter := clrSignatureMember{ordinal: 1, name: "@value", dataType: "nvarchar(20)", nullable: true}
	signature := clrSignature{typeCode: "FS", parameters: []clrSignatureMember{
		parameter, {ordinal: 0, dataType: "int", nullable: true, output: true},
	}}
	absent, err := signature.format()
	if err != nil {
		t.Fatal(err)
	}
	// Formatting sorts the scalar return before input parameters.
	signature.parameters[1].defaultValue = "NULL"
	nullDefault, err := signature.format()
	if err != nil {
		t.Fatal(err)
	}
	if absent == nullDefault || strings.Contains(absent, "DEFAULT") || !strings.Contains(nullDefault, "DEFAULT NULL") {
		t.Fatalf("no default must differ from an explicit NULL default: %q versus %q", absent, nullDefault)
	}
}

func TestCLRDefaultsPreserveTypedUnicodeAndDecimalValues(t *testing.T) {
	unicode := formatCLRDefault("nvarchar", 12, 0, 0, "Latin1_General_100_CI_AS", "雪' NULL")
	if unicode != "nvarchar(6): N'雪'' NULL' COLLATE [Latin1_General_100_CI_AS]" {
		t.Fatalf("Unicode length, quoting and collation must be retained: %q", unicode)
	}
	decimal := formatCLRDefault("decimal", 17, 38, 10, "", "1234567890123456789012345678.1234567890")
	if decimal != "decimal(38,10): 1234567890123456789012345678.1234567890" {
		t.Fatalf("decimal precision and scale must not be rounded through floating point: %q", decimal)
	}
	if decimal == formatCLRDefault("decimal", 17, 38, 9, "", "1234567890123456789012345678.1234567890") {
		t.Fatal("different default value types must not compare equal")
	}
}

func TestCLRTableSignatureOrdersMembersWithoutComparingLocalColumnIDs(t *testing.T) {
	first := clrSignatureMember{ordinal: 1, name: "first", dataType: "nvarchar(10)", nullable: true, collation: "Latin1_General_100_CI_AS"}
	second := clrSignatureMember{ordinal: 4, name: "second]", dataType: "decimal(18,4)"}
	signature := clrSignature{typeCode: "FT", columns: []clrSignatureMember{second, first}, parameters: []clrSignatureMember{
		{ordinal: 2, name: "@b", dataType: "int", nullable: true},
		{ordinal: 1, name: "@a", dataType: "varbinary(max)", readonly: true},
	}}
	got, err := signature.format()
	if err != nil {
		t.Fatal(err)
	}
	want := "([@a] varbinary(max) NOT NULL READONLY, [@b] int NULL) RETURNS TABLE ([first] nvarchar(10) COLLATE [Latin1_General_100_CI_AS] NULL, [second]]] decimal(18,4) NOT NULL)"
	if got != want {
		t.Fatalf("signature must preserve parameter/column order, names, types and modifiers:\nwant %s\n got %s", want, got)
	}
	signature.columns[1].ordinal = 2
	withoutGap, err := signature.format()
	if err != nil || withoutGap != got {
		t.Fatalf("column ID gaps must not change the visible return contract: %q (%v)", withoutGap, err)
	}
	signature.columns[0].ordinal, signature.columns[1].ordinal = 2, 1
	reordered, err := signature.format()
	if err != nil || reordered == got {
		t.Fatalf("changed return column order must be distinguishable: %q (%v)", reordered, err)
	}
}

func TestCLRSignatureRejectsMissingOrInconsistentReturns(t *testing.T) {
	for _, signature := range []clrSignature{
		{typeCode: "FS"},
		{typeCode: "FT"},
		{typeCode: "FS", parameters: []clrSignatureMember{{ordinal: 1, name: "@x", dataType: "int"}}},
		{typeCode: "FT", parameters: []clrSignatureMember{{ordinal: 0, dataType: "int"}}, columns: []clrSignatureMember{{ordinal: 1, name: "x", dataType: "int"}}},
		{typeCode: "FS", parameters: []clrSignatureMember{{ordinal: 0, dataType: "int"}, {ordinal: 0, dataType: "int"}}},
	} {
		if got, err := signature.format(); err == nil {
			t.Fatalf("incomplete/inconsistent return metadata must fail, got %q", got)
		}
	}
}
