# MSSQL Batch Compare

CLI ภาษา Go สำหรับเปรียบเทียบ schema ระหว่าง SQL Server สองฐานข้อมูล แสดงความต่างเป็นข้อความหรือรายงาน HTML และเลือกสร้าง migration script เพื่อปรับ **destination ให้ตรงกับ source** ในขอบเขตที่รองรับ

โปรแกรมอ่าน metadata เท่านั้น ไม่แก้ไขฐานข้อมูลและไม่รัน migration ให้อัตโนมัติ

## สิ่งที่รองรับ

| Object | ข้อมูลที่เปรียบเทียบ |
| --- | --- |
| User tables | ชื่อตาราง ชื่อคอลัมน์ ชนิดข้อมูล และ nullability |
| Primary keys | คอลัมน์ ลำดับคอลัมน์ ทิศทาง ASC/DESC และ clustered/nonclustered |
| SQL stored procedures | Definition, `ANSI_NULLS`, `QUOTED_IDENTIFIER` |
| Views | Definition, `ANSI_NULLS`, `QUOTED_IDENTIFIER` |
| Functions | Scalar, inline TVF และ multi-statement TVF พร้อม definition และ session settings ข้างต้น |
| Synonyms | ชื่อ target ที่อ้างอิง |

SQL definition ที่เปลี่ยนจะแสดงเป็น unified diff พร้อม context 3 บรรทัด รายงาน HTML เป็นไฟล์ standalone เปิดแบบ offline ได้

## รายงานตัวอย่าง

ตัวอย่างเหล่านี้สร้างผ่าน CLI จริงด้วย `-sql-mode normalized` จาก schema สมมติ ไม่มีข้อมูลหรือ connection credentials ของระบบจริง และเปิดดูได้โดยไม่ต้องเชื่อมต่อ SQL Server:

- [รายงานข้อความ — examples/report.txt](examples/report.txt): อ่านใน GitHub หรือ text editor ได้ทันที
- [รายงาน HTML — examples/report.html](examples/report.html): ดาวน์โหลดหรือ clone แล้วเปิดด้วย browser; ไม่ต้องมี web server หรืออินเทอร์เน็ต GitHub จะแสดง source ของไฟล์ ไม่ใช่หน้า report ที่ render แล้ว

```powershell
Start-Process .\examples\report.html
```

ตัวอย่างมี **12 differences** ครอบคลุมตารางและ stored procedure ที่มีเฉพาะฝั่งใดฝั่งหนึ่ง, ชนิดข้อมูล/nullability/คอลัมน์ที่ขาด, primary key, SQL definition ของ procedure/view/function และ target ของ synonym ส่วนตาราง `dbo.Orders` เหมือนกันทั้งสองฝั่ง จึงไม่ปรากฏเป็นรายการความต่าง

ใน SQL unified diff บรรทัด `-` คือข้อความฝั่ง **source** และ `+` คือข้อความฝั่ง **destination** ตามหัวข้อ `--- source` / `+++ destination` รายงานนี้ไม่ใช่ migration script และไม่ควรนำไปรันเป็น SQL

## ความต้องการ

- Go **1.25.0 ขึ้นไป** สำหรับ build จาก source; หากใช้ executable ที่ build แล้ว ไม่ต้องติดตั้ง Go
- การเชื่อมต่อไปยัง SQL Server ทั้ง source และ destination
- บัญชีที่มีสิทธิ์อ่าน metadata ตามหัวข้อ [สิทธิ์ฐานข้อมูล](#สิทธิ์ฐานข้อมูล)
- `sqlcmd` เป็นตัวเลือกสำหรับรัน migration ด้วยตนเอง ไม่จำเป็นสำหรับการเปรียบเทียบ

ตัวอย่างคำสั่งด้านล่างใช้ PowerShell บน Windows

## เริ่มต้นใช้งาน

### 1. Build

รันจาก directory ของโปรเจกต์:

```powershell
go mod download
go build -o mssql-batch-compare.exe .
```

### 2. ตั้งค่า connection

คัดลอกไฟล์ตัวอย่าง หากยังไม่มี `.env`:

```powershell
Copy-Item .env.example .env
```

แก้ไข `.env` ให้ตรงกับระบบของคุณ:

```dotenv
MSSQL_SOURCE_DSN='server=SOURCE_HOST;database=SourceDB;user id=compare_user;password=CHANGE_ME;encrypt=true;TrustServerCertificate=false'
MSSQL_DESTINATION_DSN='server=DESTINATION_HOST;database=DestinationDB;user id=compare_user;password=CHANGE_ME;encrypt=true;TrustServerCertificate=false'

MSSQL_GENERATE_MIGRATION=false
MSSQL_MIGRATION_OUT='migration.sql'
```

ทั้งสอง DSN ต้องระบุ `database` อย่างชัดเจน ตัวอย่างใช้การตรวจสอบ certificate จึงต้องตั้งค่า certificate ที่ client เชื่อถือได้

สำหรับ Windows Authentication ให้ละ `user id` และ `password` เพื่อใช้บัญชีปัจจุบัน:

```dotenv
MSSQL_SOURCE_DSN='server=HOST\INSTANCE;database=SourceDB;encrypt=true;TrustServerCertificate=false'
```

ข้อควรทราบเกี่ยวกับ `.env`:

- โปรแกรมหา `.env` จาก **current working directory** ไม่ใช่จากตำแหน่ง executable เสมอไป
- บันทึกเป็น UTF-8 และครอบ DSN ด้วย single quotes เพื่อรักษา `$`, `#` และ backslash ตามตัวอักษร
- Environment variables มีลำดับความสำคัญสูงกว่า `.env` แม้ค่าจาก environment จะเป็นข้อความว่าง
- ไม่จำเป็นต้องมี `.env` หากกำหนด environment variables ครบแล้ว แต่ไฟล์ที่อ่านไม่ได้หรือ syntax ผิดจะทำให้โปรแกรมหยุด
- ห้าม commit credentials จริง; `.env` ถูกกำหนดไว้ใน `.gitignore` แล้ว

### 3. เปรียบเทียบ

แสดงผลข้อความใน terminal:

```powershell
.\mssql-batch-compare.exe
```

บันทึกรายงานข้อความ:

```powershell
.\mssql-batch-compare.exe -out diff.txt
```

สร้างรายงาน HTML:

```powershell
.\mssql-batch-compare.exe -sql-mode normalized -format html -out diff.html
```

รายงานจะถูกส่งไปที่ stdout ด้วย แม้ระบุ `-out` แล้วก็ตาม การใช้ชื่อไฟล์ `.html` เพียงอย่างเดียวไม่เปลี่ยน format ต้องระบุ `-format html`

## Configuration และ CLI options

### Environment / `.env`

| ตัวแปร | ค่าเริ่มต้น | ความหมาย |
| --- | --- | --- |
| `MSSQL_SOURCE_DSN` | ไม่มี; จำเป็นต้องระบุ | Connection string ของ source |
| `MSSQL_DESTINATION_DSN` | ไม่มี; จำเป็นต้องระบุ | Connection string ของ destination |
| `MSSQL_GENERATE_MIGRATION` | `false` | ใช้ `true` เพื่อสร้าง migration SQL |
| `MSSQL_MIGRATION_OUT` | `migration.sql` | Path ของไฟล์ migration แยกจาก path รายงาน |

เมื่อปิด migration generation โปรแกรมจะไม่สร้างหรือแก้ไขไฟล์ migration เดิม Environment variables สามารถใช้ override ค่าจาก `.env` ได้ เช่น ปิด migration สำหรับการรันใน PowerShell session ปัจจุบัน:

```powershell
$env:MSSQL_GENERATE_MIGRATION = 'false'
.\mssql-batch-compare.exe
Remove-Item Env:MSSQL_GENERATE_MIGRATION
```

### CLI

| Option | ค่าเริ่มต้น | ความหมาย |
| --- | --- | --- |
| `-format text\|html` | `text` | รูปแบบรายงาน |
| `-out <path>` | ไม่เขียนไฟล์ | บันทึกรายงานเพิ่มเติมจาก stdout; เขียนทับไฟล์เดิมเมื่อสำเร็จ |
| `-sql-mode strict\|normalized` | `strict` | วิธีเปรียบเทียบ SQL definition |
| `-timeout <duration>` | `1m` | เวลารวมสำหรับเชื่อมต่อและอ่าน schema เช่น `30s`, `2m` |
| `-help` | — | แสดงวิธีใช้งาน |

Path แบบ relative อ้างอิงจาก current working directory รูปแบบรายงาน, SQL mode, timeout และ path รายงานยังคงตั้งค่าผ่าน CLI ไม่ใช่ `.env`

### SQL comparison modes

ทั้งสองโหมดปรับ line endings จาก CRLF เป็น LF ก่อนเปรียบเทียบ:

- **`strict`**: เปรียบเทียบข้อความที่เหลือทั้งหมด
- **`normalized`**: เพิ่มการทำให้ declaration `CREATE`, `ALTER`, `CREATE OR ALTER` และ spacing ก่อนชนิด module อยู่ในรูปแบบเดียวกัน

`normalized` ไม่ใช่ semantic SQL comparison: comments, literals, identifier case และ formatting ส่วนอื่นยังมีผลต่อความต่าง ส่วน diff แสดง definition เดิม จึงอาจเห็น declaration ที่ถูกละเว้นในการเปรียบเทียบอยู่ใน hunk เมื่อมีส่วนอื่นเปลี่ยนด้วย

## สร้าง migration script

เปิดใช้งานใน `.env`:

```dotenv
MSSQL_GENERATE_MIGRATION=true
MSSQL_MIGRATION_OUT='migration.sql'
```

จากนั้นรันคำสั่งเปรียบเทียบตามปกติ:

```powershell
.\mssql-batch-compare.exe -sql-mode normalized -format html -out diff.html
```

จะได้รายงาน `diff.html` และ migration `migration.sql` แยกกัน ห้ามกำหนดให้รายงานและ migration ใช้ไฟล์เดียวกัน

### ขอบเขต migration

- รองรับ SQL procedures, views, functions และ synonyms เท่านั้น
- สร้าง object ที่ขาดด้วย `CREATE` และแก้ module ที่มีอยู่ด้วย `ALTER`
- Synonym ที่เปลี่ยน target ใช้ `DROP` ตามด้วย `CREATE` ภายใน transaction
- ไม่ลบ object ที่มีเฉพาะ destination
- **ไม่สร้างหรือแก้ไข tables, schemas, ข้อมูลในตาราง หรือสิทธิ์การเข้าถึง**
- Destination ต้องมี schema และ table prerequisites ที่เข้ากันได้ก่อน หาก prerequisite table ยังขาดหรือมีความต่างในขอบเขตที่ตรวจ โปรแกรมจะหยุดให้จัดการ table ด้วยตนเอง
- Table differences ที่ไม่เกี่ยวกับ object ที่จะ migrate จะแสดงเป็นหมายเหตุใน script โดยไม่สร้าง table DDL

ดังนั้น การรัน migration สำเร็จไม่ได้หมายความว่าความต่างทั้งหมดจะหายไป เช่น table differences หรือ destination-only objects จะยังคงอยู่

### การเรียง sequence

โปรแกรมเรียงตาม dependency จาก SQL Server catalog รวมถึง target ของ synonym โดยวาง prerequisite ก่อน object ที่ใช้งาน ไม่ได้เรียงตามชื่อหรือกำหนดลำดับประเภทตายตัว ตัวอย่างกรณีที่มี dependency ต่อกัน:

```text
Function -> View -> Synonym -> Dependent view -> Stored procedure
```

หากพบ dependency cycle, reference ที่ resolve ไม่ได้, cross-database/server reference, การเปลี่ยนชนิด object หรือ schema-bound dependent ที่ขวางการแก้ไข จะหยุดพร้อมแจ้งเหตุผล และไม่ทับไฟล์ migration เดิมเมื่อวางแผนไม่สำเร็จ

การเปลี่ยนแปลงที่เสี่ยงสูญเสีย metadata เช่น indexed views, signed modules หรือ synonym ที่มี explicit permissions/extended properties จะต้องจัดการด้วยตนเองเช่นกัน

**Dynamic SQL อาจไม่มี dependency อยู่ใน catalog ต้องตรวจ sequence และ SQL ด้วยตนเองก่อนรัน**

### รัน migration ด้วยตนเอง

1. ตรวจ script, สำรองข้อมูล และทดลองกับฐานข้อมูลสำเนาก่อนใช้งานจริง
2. ใช้บัญชีที่มีสิทธิ์ DDL สำหรับ object ใน script บัญชีเปรียบเทียบไม่จำเป็นต้องมีสิทธิ์นี้
3. เชื่อมต่อ destination database/server ที่บันทึกไว้ใน script และอย่าเปิด transaction ค้างไว้
4. รัน script แล้วเปรียบเทียบซ้ำ

ตัวอย่างใช้ `sqlcmd` กับ Windows Authentication; แทน host/database ด้วย destination จริง:

```powershell
sqlcmd -S "DESTINATION_HOST" -d "DestinationDB" -E -b -f 65001 -i ".\migration.sql"
```

Script ตรวจ database/server ก่อนทำงาน ใช้ transaction เดียวร่วมกับ `XACT_ABORT` และ `TRY/CATCH` เพื่อ rollback เมื่อเกิดข้อผิดพลาด โปรแกรมไม่เปลี่ยน database ให้โดยอัตโนมัติ และไม่ยอมรันเมื่อมี transaction เปิดอยู่แล้ว

## สิทธิ์ฐานข้อมูล

บัญชีที่ใช้เชื่อมต่อต้องมี database-level `VIEW DEFINITION` บน **ทั้งสองฐานข้อมูล** หากเปิด migration generation ต้องมี `SELECT` บน `sys.sql_expression_dependencies` เพิ่มเติมด้วย

ตัวอย่างสำหรับ database user ที่มีอยู่แล้ว ให้ DBA รันในแต่ละฐานข้อมูล:

```sql
GRANT VIEW DEFINITION TO [compare_user];

-- จำเป็นเพิ่มเติมเมื่อเปิด migration generation
GRANT SELECT ON OBJECT::sys.sql_expression_dependencies TO [compare_user];
```

Object-level `DENY` อาจทำให้ metadata บางส่วนถูกซ่อน แม้จะมีสิทธิ์ระดับฐานข้อมูลแล้ว จึงควรตรวจสิทธิ์ของบัญชีให้ครบก่อนเชื่อถือผลเปรียบเทียบ

## Exit codes

| Code | ความหมาย |
| --- | --- |
| `0` | ไม่พบความต่างในขอบเขตที่เปรียบเทียบ |
| `1` | พบความต่าง; ไม่ได้หมายความว่าโปรแกรมทำงานผิดพลาด |
| `2` | เกิดข้อผิดพลาด เช่น config, connection, อ่าน metadata หรือสร้าง migration ไม่สำเร็จ |

การสร้าง migration สำเร็จยังคืนค่า `1` หากพบความต่าง เพราะโปรแกรมไม่ได้ execute script และไม่ได้เปรียบเทียบซ้ำหลังการแก้ไขฐานข้อมูล

ตัวอย่างอ่าน exit code ใน PowerShell:

```powershell
.\mssql-batch-compare.exe -format html -out diff.html
$LASTEXITCODE
```

## ข้อจำกัดและความปลอดภัย

- เปรียบเทียบชื่อแบบ case-sensitive แม้ database collation จะเป็น case-insensitive
- ไม่เปรียบเทียบ row data, column ordinal positions, ชื่อ PK constraints, defaults, identity, computed expressions, collation, non-PK indexes, FK/CHECK/UNIQUE constraints, triggers, permissions และ type/XML schema definitions
- Encrypted/unreadable definitions และ CLR/extended modules ทำให้โปรแกรมแจ้ง error ไม่ใช่ข้ามแล้วรายงานว่าเท่ากัน
- Source และ destination ถูกอ่านตามลำดับ ไม่ได้อยู่ใน snapshot ร่วมกัน ควรหลีกเลี่ยง concurrent DDL ระหว่างเปรียบเทียบ และตรวจความเปลี่ยนแปลงอีกครั้งก่อนนำ migration ไปใช้
- โปรแกรมไม่ใส่ connection credentials ลงในรายงาน แต่ SQL definitions อาจมีความลับฝังอยู่ ต้องปกป้องทั้งรายงานและ migration script
- ไฟล์ผลลัพธ์ชื่อมาตรฐาน `diff.txt`, `diff.html`, `migration.sql` ที่ root ถูก ignore แล้ว หากใช้ชื่อหรือ path อื่น ต้องจัดการ `.gitignore` และสิทธิ์ไฟล์ให้เหมาะสมเอง

## ตรวจสอบระหว่างพัฒนา

```powershell
go test ./...
go vet ./...
go build -o mssql-batch-compare.exe .
```

Unit tests ไม่ได้แทนการทดลอง migration บน SQL Server จริง ควรทดสอบ dependency ordering, rollback และเปรียบเทียบซ้ำหลัง apply กับฐานข้อมูลทดลองก่อนใช้ script ใน production
