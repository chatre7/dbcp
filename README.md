# dbcp

CLI ภาษา Go สำหรับเปรียบเทียบ schema ของ SQL Server และเลือกสร้าง migration SQL เพื่อปรับ **destination ให้ตรงกับ source** ภายในขอบเขตที่รองรับ

**โปรแกรมอ่าน metadata เท่านั้น ไม่แก้ไขฐานข้อมูลและไม่ execute migration อัตโนมัติ** รายงานเป็นข้อความหรือ HTML แบบ standalone

## เลือกวิธีใช้งาน

| ต้องการทำอะไร | อ่านหัวข้อ | การเชื่อมต่อที่ต้องใช้ |
| --- | --- | --- |
| เทียบฐานข้อมูลสองฝั่งโดยตรง | [Online compare](#online-compare) | SQL Server ทั้ง source และ destination |
| ติดตามฐานข้อมูลเดียวเทียบกับรอบก่อน | [Snapshot history](#snapshot-history) | SQL Server เฉพาะ source |
| เทียบสองฝั่งที่อยู่คนละเครือข่าย | [Offline compare](#offline-compare) | แต่ละฝั่งเชื่อมต่อเฉพาะตอน export; เครื่องกลางอ่านไฟล์เท่านั้น |
| สร้าง SQL จากฐานข้อมูลที่เชื่อมต่อได้หรือจาก snapshot | [Migration SQL](#migration-sql) | Online ใช้สองฐานข้อมูล; offline ใช้ snapshot ที่มี migration metadata ทั้งคู่ |
| รันหลายคู่หรือ export หลายฐานข้อมูล | [Batch](#batch) | ตามโหมดที่เลือก; runner รันทีละ config |
| รัน CLI โดยไม่ติดตั้ง Go บนเครื่อง host | [Docker](#docker) | ตามโหมดที่เลือก; offline ปิด network ได้ |

### สารบัญ

1. [Installation — ติดตั้งและ build](#installation)
2. [Docker — build image และ mount config/input/output](#docker)
3. [Online compare — เทียบฐานข้อมูลโดยตรง](#online-compare)
4. [Snapshot history — เก็บประวัติฐานข้อมูลเดียว](#snapshot-history)
5. [Offline compare — export และเทียบไฟล์ข้ามเครือข่าย](#offline-compare)
6. [Migration SQL — สร้างสคริปต์และนำไปใช้ด้วยตนเอง](#migration-sql)
7. [Batch — รันหลาย config](#batch)
8. [Reference — ขอบเขต รายงาน Configuration สิทธิ์ และข้อจำกัด](#reference)
9. [Development — คำสั่งตรวจสอบสำหรับผู้พัฒนา](#development)

## Installation

### ความต้องการ

- Go **1.25.0 ขึ้นไป** สำหรับ build จาก source; หากใช้ executable ที่ build แล้ว ไม่ต้องติดตั้ง Go
- การเชื่อมต่อไปยัง SQL Server ทั้ง source และ destination สำหรับโหมดเทียบสองฐานข้อมูล; `-snapshot` เชื่อมต่อเฉพาะ source ส่วนโหมดเทียบไฟล์ offline ไม่ต้องเชื่อมต่อ SQL Server
- บัญชีที่มีสิทธิ์อ่าน metadata ตามหัวข้อ [สิทธิ์ฐานข้อมูล](#สิทธิ์ฐานข้อมูล) เฉพาะเครื่องที่อ่าน SQL Server
- `sqlcmd` เป็นตัวเลือกสำหรับรัน migration ด้วยตนเอง ไม่จำเป็นสำหรับการเปรียบเทียบ

ตัวอย่างแยกเป็น **PowerShell / Windows** (`.\dbcp.exe`) และ **Bash / Linux** (`./dbcp`) โดยรันจาก root ของโปรเจกต์ เว้นแต่ระบุไว้ต่างหาก เมื่อติดตั้ง CLI เข้า PATH แล้ว สามารถใช้ `dbcp` แทน `./dbcp` หรือ `.\dbcp.exe` ได้

บน Linux ใช้ `.env` รูปแบบเดียวกัน ไม่ต้อง `source .env`; ให้โปรแกรมอ่านไฟล์เอง ตัวอย่าง connection แบบ SQL Authentication ด้านล่างใช้ได้ทั้งสองระบบ ส่วน Windows Authentication ไม่ได้ใช้ได้บน Linux โดยอัตโนมัติ

CLI คืน `1` เมื่อพบความต่าง ซึ่งไม่ใช่ error หากใช้ Bash กับ `set -e` หรือ `&&` ต้องรับ exit code ให้ถูกต้องตาม [Exit codes](#exit-codes) ส่วน `xdg-open` ใช้เฉพาะ Linux desktop ที่ติดตั้งไว้; บนเครื่อง headless ให้คัดลอก HTML ไปเปิดบนเครื่องที่มี browser

### ติดตั้งเป็นคำสั่ง `dbcp`

Repository: [github.com/chatre7/dbcp](https://github.com/chatre7/dbcp) — Go module ใช้ path เดียวกัน ติดตั้งหรืออัปเดต CLI ได้โดยไม่ต้อง clone repository:

```bash
go install github.com/chatre7/dbcp@latest
```

คำสั่ง `go install` ใช้ได้ทั้ง Bash และ PowerShell โดยติดตั้ง `dbcp` บน Linux หรือ `dbcp.exe` บน Windows ไว้ที่ `GOBIN` หรือ `bin` ของ GOPATH ตัวแรกเมื่อไม่ได้กำหนด GOBIN เพิ่ม directory นี้เข้า PATH เพื่อเรียก `dbcp` จาก directory ใดก็ได้:

**Bash / Linux — PATH สำหรับ shell ปัจจุบัน**

```bash
dbcp_bin="$(go env GOBIN)"
if [[ -z "$dbcp_bin" ]]; then
    dbcp_gopath="$(go env GOPATH)"
    dbcp_bin="${dbcp_gopath%%:*}/bin"
fi
export PATH="$dbcp_bin:$PATH"
dbcp -help
```

**PowerShell / Windows — PATH สำหรับ session ปัจจุบัน**

```powershell
$dbcpBin = go env GOBIN
if ([string]::IsNullOrWhiteSpace($dbcpBin)) {
    $dbcpGoPath = (go env GOPATH) -split ';'
    $dbcpBin = Join-Path $dbcpGoPath[0] 'bin'
}
$env:Path = "$dbcpBin;$env:Path"
dbcp -help
```

หากต้องการให้ PATH คงอยู่หลังเปิด terminal ใหม่ ให้เพิ่ม directory เดียวกันใน shell profile บน Linux หรือ User Environment Variables บน Windows โปรแกรมยังอ่าน `.env` และ resolve relative paths จาก **current working directory** ไม่ใช่ directory ที่ติดตั้ง binary จึงเลือก config ของแต่ละงานได้ด้วย directory ที่รัน

ตัวแปร `MSSQL_*`, CLI flags, snapshot format และ history เดิมยังใช้ต่อได้ ชื่อ `dbcp` ไม่ได้เพิ่มการรองรับ database engine อื่น; ขอบเขตยังเป็น SQL Server ตาม [สิ่งที่รองรับ](#สิ่งที่รองรับ)

### Build

รันจาก directory ของโปรเจกต์:

```powershell
go mod download
go build -o dbcp.exe .
```

**Bash / Linux**

```bash
go mod download
go build -o dbcp .
```

## Docker

ใช้ Docker Engine หรือ Docker Desktop ในโหมด **Linux containers** ไม่ต้องติดตั้ง Go บน host Image นี้มีเฉพาะ CLI ไม่รวม SQL Server หรือ `sqlcmd` และรันหนึ่งงานแล้วจบ ไม่ใช่ service ที่ต้องเปิด port หรือ restart อัตโนมัติ

### Build image

ใช้คำสั่งเดียวกันได้ทั้ง Bash และ PowerShell จาก root ของโปรเจกต์:

```bash
docker build -t dbcp:local .
docker run --rm --network none dbcp:local
```

หากไม่ส่ง arguments จะแสดง `-help` โดยไม่อ่าน `.env` หรือเชื่อมต่อฐานข้อมูล

- Multi-stage build สร้าง static Linux binary; runtime ใช้ `scratch` มีเฉพาะ executable, CA certificates และ timezone data ไม่มี shell หรือ package manager
- รันเป็น non-root `65532:65532` ตามค่าเริ่มต้น และใช้ working directory `/work`
- `.dockerignore` อนุญาตเฉพาะ build inputs ไม่ส่ง `.env`, `data/`, `runs/`, รายงาน, migration scripts, executable หรือ `.git` เข้า build context
- Flags ของ Docker เช่น `--mount`, `--user`, `-e`, `--network` อยู่ **ก่อนชื่อ image**; flags ของ CLI เช่น `-snapshot` และ `-out` อยู่ **หลังชื่อ image**

### Config และไฟล์ผลลัพธ์

เตรียม `.env` ตาม [ตั้งค่า connection](#ตั้งค่า-connection) แล้ว mount แบบ read-only ที่ `/work/.env` ให้โปรแกรมอ่านเอง **ไม่ใช้ `docker run --env-file .env` กับไฟล์ตัวอย่างที่ครอบ DSN ด้วย single quotes** เพราะ Docker ไม่ใช้ parser แบบเดียวกับโปรแกรมและอาจส่ง quote เป็นส่วนหนึ่งของค่า

Mount โฟลเดอร์ output ที่ `/data` และระบุ path ภายใน container เช่น `/data/diff.html` หรือ `-data-dir /data` เพื่อให้ผลลัพธ์ยังอยู่บน host หลัง `--rm` ห้ามพึ่งไฟล์ที่เขียนไว้เฉพาะภายใน container

บน Linux ตัวอย่างใช้ `--user "$(id -u):$(id -g)"` เพื่ออ่าน `.env` ที่ตั้ง permission `600` และเขียน output ด้วยเจ้าของเดียวกับผู้เรียก หากใช้ค่าเริ่มต้นของ image ต้องให้ UID/GID `65532:65532` มีสิทธิ์อ่าน config/input และเขียน output อย่าแก้ permission error ด้วย `--privileged` หรือเปิดสิทธิ์ credentials ให้ทุกคน

### Online compare ผ่าน Docker

**Bash / Linux**

```bash
mkdir -p ./data
docker_exit=0
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
    --user "$(id -u):$(id -g)" \
    --mount "type=bind,source=$PWD/.env,target=/work/.env,readonly" \
    --mount "type=bind,source=$PWD/data,target=/data" \
    -e MSSQL_GENERATE_MIGRATION=false \
    dbcp:local \
    -sql-mode normalized -format html -out /data/diff.html || docker_exit=$?
printf 'Container exit code: %s\n' "$docker_exit"
```

**PowerShell / Windows — Docker Desktop**

```powershell
New-Item -ItemType Directory -Path .\data -Force | Out-Null
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges `
    --mount "type=bind,source=$($PWD.Path)/.env,target=/work/.env,readonly" `
    --mount "type=bind,source=$($PWD.Path)/data,target=/data" `
    -e MSSQL_GENERATE_MIGRATION=false `
    dbcp:local `
    -sql-mode normalized -format html -out /data/diff.html
$LASTEXITCODE
```

รายงานอยู่ที่ `data/diff.html` บน host หากต้องการ **online migration** ให้เปลี่ยน `-e MSSQL_GENERATE_MIGRATION=false` เป็น `true` และเพิ่ม `-e MSSQL_MIGRATION_OUT=/data/migration.sql` ก่อนชื่อ image จะได้ script ที่ `data/migration.sql` โดยยังไม่ execute SQL

### Snapshot และ export สำหรับ offline migration

ใช้ config/output mounts และ Docker security flags จากคำสั่ง online ข้างต้น โดยคง `MSSQL_GENERATE_MIGRATION=false` แล้วเปลี่ยน arguments **หลังชื่อ image** เป็น:

```text
-snapshot -include-migration -data-dir /data -sql-mode normalized -format html -timeout 3m
```

เชื่อมต่อเฉพาะ `MSSQL_SOURCE_DSN` และเก็บประวัติที่ `data/ddmmyyhhmmss/<database-id>/` บน host หากต้องการ history เพื่อเทียบอย่างเดียว ให้ละ `-include-migration`; หากเปิด flag นี้ บัญชีต้องมีสิทธิ์ dependency catalog เพิ่มตาม [สิทธิ์ฐานข้อมูล](#สิทธิ์ฐานข้อมูล)

Container ใช้เวลา UTC ตามค่าเริ่มต้น หากต้องการชื่อโฟลเดอร์ตามเวลาไทย ให้เพิ่ม `-e TZ=Asia/Bangkok` ก่อนชื่อ image เวลา capture ใน JSON/รายงานยังคงเป็น UTC

### Offline compare และ migration โดยไม่ใช้ network

วาง snapshot สองไฟล์ที่ `data/offline/source.json` และ `data/offline/destination.json` แล้ว mount input แบบ read-only ส่วนผลลัพธ์เก็บแยกที่ `data/docker/` ไม่ต้อง mount `.env` และใช้ `--network none` ได้

**Bash / Linux**

```bash
mkdir -p ./data/docker
docker_exit=0
docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
    --user "$(id -u):$(id -g)" \
    --mount "type=bind,source=$PWD/data/offline,target=/snapshots,readonly" \
    --mount "type=bind,source=$PWD/data/docker,target=/data" \
    dbcp:local \
    -source-snapshot /snapshots/source.json \
    -destination-snapshot /snapshots/destination.json \
    -sql-mode normalized -format html -out /data/diff.html || docker_exit=$?
printf 'Container exit code: %s\n' "$docker_exit"
```

**PowerShell / Windows — Docker Desktop**

```powershell
New-Item -ItemType Directory -Path .\data\docker -Force | Out-Null
docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges `
    --mount "type=bind,source=$($PWD.Path)/data/offline,target=/snapshots,readonly" `
    --mount "type=bind,source=$($PWD.Path)/data/docker,target=/data" `
    dbcp:local `
    -source-snapshot /snapshots/source.json `
    -destination-snapshot /snapshots/destination.json `
    -sql-mode normalized -format html -out /data/diff.html
$LASTEXITCODE
```

หาก snapshot **ทั้งคู่** export ด้วย `-include-migration` แล้ว ให้เพิ่ม `-migration-out /data/migration.sql` ใน arguments ของ CLI จะได้ SQL แยกจาก HTML โดยไม่เชื่อมต่อฐานข้อมูล ไม่ execute SQL และยังต้องตรวจ destination drift ก่อนนำไปใช้ตาม [Migration SQL](#migration-sql)

### Network, TLS และ exit codes

- `localhost` ใน DSN หมายถึง container เอง ไม่ใช่เครื่อง host หาก SQL Server อยู่บน host ให้ใช้ `host.docker.internal` บน Docker Desktop; บน Docker Engine Linux เพิ่ม `--add-host host.docker.internal:host-gateway` ก่อนชื่อ image และตรวจว่า SQL Server รับ TCP จาก network นี้ได้
- สำหรับ SQL Server ในเครือข่ายอื่น ให้ใช้ DNS/host และ TCP port ที่ container เชื่อมถึง พร้อมตั้ง firewall/certificate ให้ตรงกัน Linux container ไม่รับ Windows Authentication ของเครื่อง host โดยอัตโนมัติ
- Image มี public CA certificates หากองค์กรใช้ private CA ให้ mount PEM certificate แบบ read-only เช่น `/certs/company-ca.pem` แล้วเพิ่ม `-e SSL_CERT_FILE=/certs/company-ca.pem` ก่อนชื่อ image โดยใช้ DSN `encrypt=true;TrustServerCertificate=false` ตามเดิม
- รหัสผ่านต้องอยู่ในไฟล์ config ที่ป้องกันสิทธิ์ ไม่ใส่ใน Dockerfile, build arguments หรือ image ไฟล์ snapshot/HTML/SQL อาจมี SQL definitions ที่เป็นความลับ จึงต้องป้องกัน bind mounts ด้วย
- CLI ยังคืน `0` / `1` / `2` ตาม [Exit codes](#exit-codes); `1` คือพบความต่าง ไม่ใช่ container ล้มเหลว ส่วน code `125`–`127` อาจเป็นความผิดพลาดตอน Docker เริ่ม container จึงไม่ควรตั้ง restart policy ตาม exit code
- PowerShell batch runner เดิมรับ path ของ native executable ไม่ใช่ image name หากต้องการ batch ด้วย Docker ให้รัน `docker run` แยกแต่ละ config โดยเปลี่ยน config/output mounts ไม่ mount ทั้ง repository เพื่อหลีกเลี่ยงการเปิดเผย credentials ที่ไม่เกี่ยวข้อง

## Online compare

ใช้เมื่อเครื่องที่รัน CLI เชื่อมต่อได้ทั้ง source และ destination หัวข้อนี้เปรียบเทียบและออกรายงานเท่านั้น; การเปิดสร้าง SQL อยู่ที่ [Migration SQL](#migration-sql)

### ตั้งค่า connection

ขั้นตอนตั้งค่า connection นี้ใช้กับการอ่าน SQL Server โดยตรง หากมีไฟล์ snapshot อยู่แล้ว ให้ข้ามไปที่ [Offline compare](#offline-compare) โดยไม่ต้องสร้าง `.env`

คัดลอกไฟล์ตัวอย่าง หากยังไม่มี `.env`:

```powershell
Copy-Item .env.example .env
```

**Bash / Linux** — ไม่ทับ `.env` เดิม และจำกัดสิทธิ์ไฟล์ credentials:

```bash
if [[ ! -e .env ]]; then
    (umask 077; cp .env.example .env)
fi
chmod 600 .env
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

### เปรียบเทียบและออกรายงาน

แสดงผลข้อความใน terminal:

```powershell
.\dbcp.exe
```

บันทึกรายงานข้อความ:

```powershell
.\dbcp.exe -out diff.txt
```

สร้างรายงาน HTML:

```powershell
.\dbcp.exe -sql-mode normalized -format html -out diff.html
```

**Bash / Linux** — เลือกรันตามรูปแบบรายงานที่ต้องการ หลังตั้ง `.env` ข้างต้น:

```bash
# Text to terminal
./dbcp

# Text report
./dbcp -out diff.txt

# HTML report
./dbcp -sql-mode normalized -format html -out diff.html
```

รายงานจะถูกส่งไปที่ stdout ด้วย แม้ระบุ `-out` แล้วก็ตาม การใช้ชื่อไฟล์ `.html` เพียงอย่างเดียวไม่เปลี่ยน format ต้องระบุ `-format html`

## Snapshot history

ใช้ `-snapshot` เพื่ออ่านฐานข้อมูลจาก `MSSQL_SOURCE_DSN` แล้วเทียบกับ snapshot ล่าสุดของ **server/database เดียวกัน** ไม่ใช้ `MSSQL_DESTINATION_DSN` และต้องตั้ง `MSSQL_GENERATE_MIGRATION=false` โหมดนี้ไม่สร้างหรือรัน migration และไม่อ่าน row data

```powershell
.\dbcp.exe -snapshot -sql-mode normalized -format html -timeout 3m
```

**Bash / Linux** — ตั้ง `MSSQL_SOURCE_DSN` ใน `.env`; ค่าด้านหน้าคำสั่งปิด migration เฉพาะ process นี้:

```bash
MSSQL_GENERATE_MIGRATION=false ./dbcp \
    -snapshot -sql-mode normalized -format html -timeout 3m
```

เลือก history root บน Linux ได้ด้วย `-data-dir "$HOME/private/schema-history"`

### ไฟล์ผลลัพธ์และการเลือก baseline

ผลลัพธ์ถูกเก็บใต้ `data/ddmmyyhhmmss/` อ้างอิง current working directory เช่น:

```text
data/
├── 010726093015/                  # ตัวอย่าง: 1 ก.ค. 2026 เวลา 09:30:15
│   └── <database-id>/
│       ├── snapshot.json
│       └── report.html
└── 020726093020/
    └── <database-id>/
        ├── snapshot.json
        └── report.html
```

- `ddmmyyhhmmss` ใช้นาฬิกาท้องถิ่นของเครื่องที่รัน; เวลา capture ใน JSON/รายงานเก็บเป็น UTC
- `<database-id>` คือ SHA-256 ของชื่อ server/database ที่อ่านจาก SQL Server ไม่ใช้ DSN หรือ credentials ฐานข้อมูลต่างกันจึงไม่ปะปนกัน แม้อยู่ในโฟลเดอร์เวลาเดียวกัน
- `snapshot.json` เก็บ metadata/SQL definitions/CLR fingerprints ในขอบเขต comparator เดิม ไม่ใช่ database backup และไม่มี row data, DLL binary หรือ connection string
- ไฟล์ที่ export ใหม่ใช้ format version 2; `-include-migration` เพิ่ม migration payload โดยไม่เปลี่ยนขอบเขตการเปรียบเทียบ ไฟล์ version 1 และ version 2 ที่ไม่มี payload ยังใช้เทียบและอ่านประวัติได้ แต่ต้อง export ใหม่พร้อม `-include-migration` หากจะสร้าง migration; version ที่ไม่รองรับหรือ payload ที่ไม่สมบูรณ์จะถูกปฏิเสธ
- รอบแรกสร้าง baseline และคืน `0` โดยแจ้งชัดเจนว่ายังไม่ได้เปรียบเทียบย้อนหลัง รอบถัดไปคืน `0` เมื่อไม่ต่าง หรือ `1` เมื่อพบความต่าง
- รายงานใช้ **Source = snapshot ก่อนหน้า / Destination = schema ปัจจุบัน** โดย `ADDED` และ `REMOVED` อ้างอิงจากรอบก่อน มีทั้ง SQL diff และเวลา `create_date`/`modify_date` ของ objects ปัจจุบัน เรียงล่าสุดก่อน
- เลือก baseline จาก capture timestamp ในไฟล์ ไม่เรียงชื่อโฟลเดอร์แบบข้อความ จึงรองรับการข้ามเดือน/ปี
- ทุกครั้งที่สำเร็จเก็บ snapshot ใหม่ รวมถึงรอบที่ไม่มีความต่าง หากจับฐานข้อมูลเดียวกันซ้ำภายในวินาทีเดียวกันจะ error โดยไม่ทับ snapshot เดิม ให้รันใหม่ในวินาทีถัดไป
- ไฟล์ snapshot เป็นตัวบอกว่ารอบนั้นบันทึกครบแล้ว หาก baseline เสียหรือเป็น format version ที่ไม่รองรับ จะ error ไม่เริ่ม baseline ใหม่เงียบ ๆ
- `-format text` สร้าง `report.txt` แทน HTML; `-out` เป็นสำเนารายงานเพิ่มเติมนอก `-data-dir` ไม่จำเป็นต้องระบุเพื่อเก็บประวัติ

เปลี่ยนที่เก็บได้ด้วย `-data-dir 'D:\private\schema-history'` รักษาโฟลเดอร์นี้ไว้ระหว่างรอบ; หากลบประวัติทั้งหมดหรือเปลี่ยนชื่อ server/database จะเริ่ม baseline ใหม่ `/data/` ในโปรเจกต์ถูก ignore แล้ว หากใช้ path อื่นต้องป้องกันไฟล์เอง เพราะ SQL definitions อาจมีข้อมูลอ่อนไหวฝังอยู่

ต้องการ export เพื่อสร้าง SQL ภายหลัง ดู [เตรียม snapshot สำหรับ offline migration](#เตรียม-snapshot-สำหรับ-offline-migration); ต้องการติดตามหลายฐานข้อมูล ดู [Batch snapshot](#batch-snapshot)

### ขอบเขตของประวัติ

Snapshot แสดงความต่างสุทธิระหว่างรอบ ไม่ใช่ DDL audit: ไม่รู้ว่าใครแก้หรือเวลาแก้ที่แน่นอน ไม่เห็น object ที่สร้างแล้วลบระหว่างสองรอบ และไม่เห็นการแก้แล้วเปลี่ยนกลับก่อน capture `modify_date` เป็นเวลา metadata ของ SQL Server ซึ่งไม่มี timezone และอาจเปลี่ยนจาก index DDL โดย schema ในขอบเขตที่เปรียบเทียบยังเหมือนเดิม จึงใช้เป็นข้อมูลประกอบ ไม่ใช้ตัดสินว่ามี schema diff

Metadata ถูกอ่านหลาย query ไม่ใช่ transactionally consistent snapshot ควรหลีกเลี่ยง DDL ระหว่างรัน แม้โปรแกรมตรวจความสอดคล้องของรายชื่อ/ชนิด objects แล้ว หากต้องการ log ทุกเหตุการณ์พร้อมผู้แก้ ต้องติดตั้งระบบ audit แยกต่างหาก

## Offline compare

ใช้เมื่อ SQL Server สองฝั่งอยู่คนละเครือข่ายที่เชื่อมถึงกันไม่ได้: **เครื่อง A** export ต้นทาง, **เครื่อง B** export ปลายทาง แล้วส่งเฉพาะ `snapshot.json` ไปยัง **เครื่อง C** เพื่อเปรียบเทียบ โดยไม่ต้องเปิดการเชื่อมต่อระหว่างเครือข่าย

ขั้นตอนนี้เป็น **compare-only** หากต้องการสร้าง SQL ด้วย ให้เพิ่ม `-include-migration` ตอน export **ทั้งสองฝั่ง** ตาม [การเตรียม offline migration](#เตรียม-snapshot-สำหรับ-offline-migration) ก่อนส่งไฟล์

### 1. เครื่อง A: export ฝั่ง source

วาง executable แล้วรันจาก directory ที่ต้องการเก็บประวัติ ตัวอย่างใช้ Windows Authentication; เปลี่ยน host/database เป็นค่าจริงและใช้บัญชีที่มี `VIEW DEFINITION`:

```powershell
$env:MSSQL_SOURCE_DSN = 'server=SOURCE_HOST;database=SourceDB;encrypt=true;TrustServerCertificate=false'
$env:MSSQL_GENERATE_MIGRATION = 'false'
.\dbcp.exe -snapshot -sql-mode normalized -format html -timeout 3m
$LASTEXITCODE
```

**Bash / Linux** — ตั้ง `MSSQL_SOURCE_DSN` ใน `.env` เป็นฐานข้อมูลต้นทางของเครือข่าย A โดยใช้รูปแบบ SQL Authentication ตาม [ตั้งค่า connection](#ตั้งค่า-connection):

```bash
capture_exit=0
MSSQL_GENERATE_MIGRATION=false ./dbcp \
    -snapshot -sql-mode normalized -format html -timeout 3m || capture_exit=$?
printf 'Capture exit code: %s\n' "$capture_exit"
```

ใช้ DSN ของฐานข้อมูลภายในเครือข่าย A เท่านั้น ไม่ต้องระบุ destination ผล export อยู่ที่ `data/ddmmyyhhmmss/<database-id>/snapshot.json` ตามโหมด `-snapshot` เดิม: รอบแรกคืน `0`, รอบถัดไปอาจคืน `1` เมื่อ schema เปลี่ยนจากประวัติของเครื่อง A ซึ่งยังถือว่า export สำเร็จ; หากคืน `2` ให้แก้ error ก่อนส่งไฟล์

### 2. เครื่อง B: export ฝั่ง destination

ทำแยกกันภายในเครือข่าย B โดย **ยังใช้ `MSSQL_SOURCE_DSN`** เพื่อเลือกฐานข้อมูลที่จะ export แม้ไฟล์นี้จะเป็น destination ตอนเปรียบเทียบ บัญชีต้องมี `VIEW DEFINITION` เช่นเดียวกับ A:

```powershell
$env:MSSQL_SOURCE_DSN = 'server=DESTINATION_HOST;database=DestinationDB;encrypt=true;TrustServerCertificate=false'
$env:MSSQL_GENERATE_MIGRATION = 'false'
.\dbcp.exe -snapshot -sql-mode normalized -format html -timeout 3m
$LASTEXITCODE
```

**Bash / Linux** — ตั้ง `MSSQL_SOURCE_DSN` ใน `.env` บนเครื่อง B ให้ชี้ฐานข้อมูล **ปลายทาง** แล้ว export ด้วยคำสั่งเดียวกัน:

```bash
capture_exit=0
MSSQL_GENERATE_MIGRATION=false ./dbcp \
    -snapshot -sql-mode normalized -format html -timeout 3m || capture_exit=$?
printf 'Capture exit code: %s\n' "$capture_exit"
```

เลือกไฟล์ `data/ddmmyyhhmmss/<database-id>/snapshot.json` ของรอบที่ต้องการบนเครื่อง B เช่นเดียวกับ A แต่ละเครื่องใช้ `.env` ของตัวเองแทน environment variables ได้ โดยต้องปิด migration และตั้ง source ให้ถูกฐานข้อมูล

### 3. ส่งเฉพาะ snapshot ไปเครื่อง C

คัดลอก **เฉพาะ `snapshot.json` ของรอบที่เลือกจากแต่ละเครื่อง** ผ่านช่องทางที่องค์กรอนุญาตและป้องกันการเข้าถึง/แก้ไข เช่น สื่อถอดได้ที่เข้ารหัส ไม่ต้องส่ง `.env`, credentials, รายงานประวัติ หรือทั้งโฟลเดอร์โปรเจกต์ แยกไฟล์ของ A และ B ให้ชัดเจนเพื่อไม่สลับทิศทาง

Snapshot exporter ไม่บันทึก row data หรือ connection credentials แต่ SQL definitions อาจมีความลับฝังอยู่ จึงต้องปกป้อง snapshot และรายงานเหมือนข้อมูลอ่อนไหว การอยู่ใน `.gitignore` ไม่ใช่การเข้ารหัสหรือการควบคุมสิทธิ์

ตัวอย่างบนเครื่อง C สมมติว่าสื่อที่ได้รับมีไฟล์ของ A ที่ `E:\schema-transfer\source\snapshot.json` และของ B ที่ `E:\schema-transfer\destination\snapshot.json` ให้คัดลอกและตั้งชื่อใหม่ใต้ `data/offline/` ของโปรเจกต์ ซึ่ง `/data/` ถูก ignore ไว้แล้ว:

```powershell
New-Item -ItemType Directory -Path .\data\offline -Force | Out-Null
Copy-Item -LiteralPath 'E:\schema-transfer\source\snapshot.json' -Destination .\data\offline\source.json
Copy-Item -LiteralPath 'E:\schema-transfer\destination\snapshot.json' -Destination .\data\offline\destination.json
```

**Bash / Linux** — แทน `/media/schema-transfer` ด้วย mount point ของสื่อที่ได้รับ:

```bash
(
    umask 077
    mkdir -p ./data/offline &&
    cp /media/schema-transfer/source/snapshot.json ./data/offline/source.json &&
    cp /media/schema-transfer/destination/snapshot.json ./data/offline/destination.json
)
```

### 4. เครื่อง C: เปรียบเทียบและเปิด HTML

เครื่อง C ต้องมีเพียง executable และไฟล์ snapshot สองไฟล์สำหรับการรัน ไม่ต้องมี SQL Server, `sqlcmd`, DSN หรือ `.env` โหมดนี้เลือกทำงานก่อนโหลด config จึงไม่อ่าน `.env` หรือค่า environment สำหรับฐานข้อมูล/migration และไม่เชื่อมต่อ SQL Server:

```powershell
.\dbcp.exe `
    -source-snapshot .\data\offline\source.json `
    -destination-snapshot .\data\offline\destination.json `
    -sql-mode normalized -format html -out .\data\offline\diff.html
$compareExitCode = $LASTEXITCODE
$compareExitCode
if ($compareExitCode -in 0, 1) {
    Start-Process .\data\offline\diff.html
}
```

**Bash / Linux**

```bash
compare_exit=0
./dbcp \
    -source-snapshot ./data/offline/source.json \
    -destination-snapshot ./data/offline/destination.json \
    -sql-mode normalized -format html -out ./data/offline/diff.html || compare_exit=$?
printf 'Compare exit code: %s\n' "$compare_exit"
if [[ $compare_exit == 0 || $compare_exit == 1 ]]; then
    # Optional: Linux desktop only
    xdg-open ./data/offline/diff.html
fi
```

- ต้องระบุ `-source-snapshot` และ `-destination-snapshot` **คู่กัน** และห้ามใช้ร่วมกับ `-snapshot`
- Source/Destination เป็นไปตามไฟล์ที่ระบุ ไม่สลับให้ตามเวลา ใช้เทียบต่าง server/database ได้ หรือระบุ snapshot เก่าเป็น source และ snapshot ใหม่ของฐานข้อมูลเดียวกันเป็น destination เพื่อดูความต่างย้อนหลังได้
- ใช้ขอบเขต schema เดิมตาม [สิ่งที่รองรับ](#สิ่งที่รองรับ) และ [ข้อจำกัดและความปลอดภัย](#ข้อจำกัดและความปลอดภัย) รวมถึง `-sql-mode strict|normalized` ไม่เปรียบเทียบ row data หรือขยายเป็น database backup comparison
- รายงานระบุชัดว่าเป็น **offline** พร้อม server/database และเวลา capture **UTC ของทั้งสองไฟล์** ผลสะท้อนเฉพาะข้อมูลที่ capture ไว้ ไม่ยืนยัน schema ปัจจุบันบน SQL Server และไฟล์ทั้งสองอาจถูกจับคนละเวลา ข้อจำกัดเรื่องหลาย query และ concurrent DDL ขณะ export ยังมีผล
- ตรวจ JSON, format version และ metadata ของ snapshot ก่อนเปรียบเทียบ หากไฟล์เสีย ข้อมูลที่จำเป็นไม่ถูกต้อง หรือ version ไม่รองรับ จะหยุดด้วย error ไม่ข้ามข้อมูลแล้วรายงานว่าเท่ากัน และไม่เชื่อมต่อฐานข้อมูลเพื่อเติมข้อมูล
- ค่าเริ่มต้นเป็น **compare-only** แม้ environment จะเปิด migration ไว้ ต้องระบุ `-migration-out` อย่างชัดเจนจึงสร้าง script ตามหัวข้อ [Offline migration](#สร้าง-sql-จาก-snapshot-offline) ไม่มีการ execute SQL, สร้าง snapshot history ใหม่ หรือเลื่อน baseline อัตโนมัติ ไฟล์ใต้ `data/offline/` เป็นเพียง input/output ที่ผู้ใช้เลือก
- `-format text` ใช้รายงานข้อความแทน HTML ได้ ทั้งสอง format ส่งรายงานไป stdout เสมอ; `-out` เป็นสำเนาเพิ่มเติม และต้องไม่ชี้ไปยัง input ฝั่งใดฝั่งหนึ่ง แม้ใช้ relative path, symlink หรือ hardlink คนละชื่อที่อ้างถึงไฟล์เดียวกัน
- Exit code `0` = ไม่ต่าง, `1` = พบความต่าง, `2` = error เมื่อเกิด error อย่าใช้รายงานเก่าที่อาจค้างจากรอบก่อน

## Migration SQL

**เป็นขั้นตอนที่เลือกเปิดเพิ่ม ไม่ใช่การเปรียบเทียบตามค่าเริ่มต้น** เลือกวิธีสร้างสคริปต์ตาม input ด้านล่าง ทั้งสองวิธีใช้ planner และข้อจำกัดเดียวกัน และไม่ execute SQL ให้

### สร้าง SQL จากฐานข้อมูลโดยตรง

ตั้งค่า source/destination ตาม [Online compare](#online-compare) แล้วเปิดใช้งานใน `.env`:

```dotenv
MSSQL_GENERATE_MIGRATION=true
MSSQL_MIGRATION_OUT='migration.sql'
```

จากนั้นรันคำสั่งเปรียบเทียบตามปกติ:

```powershell
.\dbcp.exe -sql-mode normalized -format html -out diff.html
```

**Bash / Linux** — ใช้ค่าชั่วคราวเฉพาะคำสั่งนี้แทนการแก้ migration settings ใน `.env` ได้ โดยยังอ่าน source/destination DSN จาก `.env`:

```bash
MSSQL_GENERATE_MIGRATION=true MSSQL_MIGRATION_OUT=migration.sql \
    ./dbcp -sql-mode normalized -format html -out diff.html
```

จะได้รายงาน `diff.html` และ migration `migration.sql` แยกกัน ห้ามกำหนดให้รายงานและ migration ใช้ไฟล์เดียวกัน

### เตรียม snapshot สำหรับ offline migration

ต้อง export พร้อม migration metadata **ทั้ง source และ destination** ภายในเครือข่ายของแต่ละฝั่ง โดยตั้ง `MSSQL_SOURCE_DSN` เป็นฐานข้อมูลที่กำลัง export แม้เป็นฝั่ง destination และตั้ง `MSSQL_GENERATE_MIGRATION=false`:

```powershell
$env:MSSQL_GENERATE_MIGRATION = 'false'
.\dbcp.exe -snapshot -include-migration -sql-mode normalized -format html -timeout 3m
```

**Bash / Linux** — ตั้ง source DSN ของแต่ละเครื่องใน `.env` ก่อนรัน:

```bash
MSSQL_GENERATE_MIGRATION=false ./dbcp \
    -snapshot -include-migration -sql-mode normalized -format html -timeout 3m
```

`-include-migration` ใช้ได้เฉพาะกับ `-snapshot` และเก็บ schemas, object inventory, dependencies กับเหตุผลที่ไม่ปลอดภัยสำหรับ migration เพิ่มเติม ไม่สร้างหรือ execute SQL และยังเชื่อมต่อเฉพาะ source บัญชี export ต้องมี `SELECT` บน `sys.sql_expression_dependencies` เพิ่มจาก `VIEW DEFINITION`

ใช้ขั้นตอนเลือกฐานข้อมูลและส่งไฟล์ตาม [Offline compare](#offline-compare) แต่เพิ่ม flag นี้ในคำสั่ง export ของทั้งเครื่อง A และ B; หาก export หลายฐานข้อมูล ดู [Batch export สำหรับ migration](#batch-export-สำหรับ-migration)

### สร้าง SQL จาก snapshot offline

เพิ่ม `-migration-out` พร้อม flags ของ input ทั้งคู่เพื่อ opt in การสร้าง script โดยไม่ต้องสร้างหรือโหลด `.env`, ไม่ใช้ DSN/environment migration settings และไม่เชื่อมต่อ SQL Server:

```powershell
.\dbcp.exe `
    -source-snapshot data/offline/source.json `
    -destination-snapshot data/offline/destination.json `
    -sql-mode normalized -format html -out data/offline/diff.html `
    -migration-out data/offline/migration.sql
$LASTEXITCODE
```

**Bash / Linux**

```bash
migration_exit=0
./dbcp \
    -source-snapshot ./data/offline/source.json \
    -destination-snapshot ./data/offline/destination.json \
    -sql-mode normalized -format html -out ./data/offline/diff.html \
    -migration-out ./data/offline/migration.sql || migration_exit=$?
printf 'Generation exit code: %s\n' "$migration_exit"
```

- `-migration-out` ใช้ได้เฉพาะเมื่อระบุ `-source-snapshot` และ `-destination-snapshot` คู่กัน ไม่ใช้ร่วมกับ `-snapshot` หรือโหมด online
- **ทั้งสองไฟล์ต้องมี migration metadata ที่ครบถ้วนและผ่านการตรวจสอบ** จาก `-snapshot -include-migration` ไฟล์ version 1 หรือ snapshot แบบ compare-only ยังเปรียบเทียบได้ แต่สร้าง script ไม่ได้ ต้องกลับไป export ใหม่จากทั้งสองเครือข่ายตามความจำเป็น โปรแกรมไม่เติม dependency ที่ขาดด้วยการเชื่อมต่อฐานข้อมูลและไม่ข้าม payload ที่เสีย
- ใช้ planner และข้อจำกัดเดิม: dependency ที่จำเป็นขาด/resolve ไม่ได้, cycle, cross-database/server reference, schema-bound dependent หรือการเปลี่ยนแปลงที่ไม่ปลอดภัยทำให้ generation หยุด ก่อนเขียนทับรายงานหรือ script เดิม การมี migration metadata ไม่ได้ยืนยันว่าแผนนั้นปลอดภัยเสมอไป
- Path ของรายงานและ script ต้องแยกจากกันและห้ามทับ input ทั้งสองไฟล์หรือ `.env` รวมถึงชื่ออื่นที่อ้างถึงไฟล์เดียวกันผ่าน symlink/hardlink
- รองรับเฉพาะ SQL procedures/views/functions และ synonyms ในขอบเขตเดิม ไม่เพิ่ม table/CLR migration; tables, schemas, assembly/CLR และส่วนที่ไม่รองรับต้องจัดการด้วยตนเองตาม [ขอบเขต migration](#ขอบเขต-migration)
- **โปรแกรมสร้างไฟล์เท่านั้น ไม่ execute SQL** ตรวจ script และ dependency โดยเฉพาะ dynamic SQL ด้วยตนเอง สำรองข้อมูลและทดลองบนสำเนาก่อนนำไปใช้
- **Destination อาจเปลี่ยนไปแล้วหลังเวลา capture** ต้องตรวจสถานะปัจจุบันหรือ export ใหม่ก่อน apply Script ตรวจชื่อ destination server/database แบบตรงกันตามค่าที่ capture แต่การตรวจ identity นี้ **ไม่ใช่ full drift check** และไม่ยืนยันว่า schema ปัจจุบันยังตรงกับ snapshot
- เมื่อวางแผนไม่สำเร็จจะคืน `2` และไม่ทับรายงาน/script เดิม อย่านำไฟล์ที่ค้างจากรอบก่อนมาใช้เสมือนเป็นผลใหม่; generation สำเร็จยังคืน `1` เมื่อมีความต่าง เพราะยังไม่ได้ apply

### ขอบเขต migration

- รองรับ SQL procedures, views, functions และ synonyms เท่านั้น
- CLR functions รองรับเฉพาะการเปรียบเทียบ ไม่สร้าง assembly/CLR DDL; หาก CLR function ที่ source ต้องการยังขาดหรือแตกต่าง ต้อง migrate ด้วยตนเองก่อน ส่วน CLR prerequisite ที่มีอยู่และตรงกันแล้วใช้ประกอบการเรียง SQL migration ได้
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

**Bash / Linux** — หากติดตั้ง `sqlcmd` และใช้ SQL Authentication ให้ระบุ user แต่ไม่ใส่ `-P` เพื่อให้ prompt รับรหัสผ่าน ไม่ใส่รหัสผ่านจริงใน command history:

```bash
sqlcmd -S 'DESTINATION_HOST' -d 'DestinationDB' -U 'migration_user' \
    -b -f 65001 -i './migration.sql'
```

ใช้ TLS/certificate ที่เชื่อถือได้ตามการตั้งค่า `sqlcmd` ของระบบ ไม่ปิดการตรวจ certificate เพื่อข้ามข้อผิดพลาด

Script ตรวจชื่อ database/server แบบตรงกันก่อนทำงาน ใช้ transaction เดียวร่วมกับ `XACT_ABORT` และ `TRY/CATCH` เพื่อ rollback เมื่อเกิดข้อผิดพลาด โปรแกรมไม่เปลี่ยน database ให้โดยอัตโนมัติ และไม่ยอมรันเมื่อมี transaction เปิดอยู่แล้ว การตรวจชื่อไม่ใช่ full drift check โดยเฉพาะ script จาก snapshot ซึ่ง destination อาจเปลี่ยนหลัง capture ต้องตรวจ schema ปัจจุบันก่อน apply ด้วยตนเอง

## Batch

ตัวอย่าง [examples/batch/run.ps1](examples/batch/run.ps1) ใช้ CLI เดิมรัน **ทีละคู่** บน Windows PowerShell 5.1 หรือ PowerShell 7 ไม่ใช่ flag batch ใหม่ใน executable แต่ละโฟลเดอร์ใต้ `runs` คือหนึ่งคู่ Source → Destination หากต้องการ source เดียวเทียบหลาย destination ให้ใช้ source DSN เดียวกันในแต่ละคู่

### เตรียม config แยกต่อฐานข้อมูล

หลัง build executable แล้ว รันจาก root ของโปรเจกต์เพื่อสร้างตัวอย่าง ERP และ CRM โดยไม่ทับ `.env` ที่มีอยู่:

```powershell
foreach ($pair in @('erp', 'crm')) {
    New-Item -ItemType Directory -Path ".\runs\$pair" -Force | Out-Null
    if (-not (Test-Path -LiteralPath ".\runs\$pair\.env")) {
        Copy-Item ".\examples\batch\$pair.env.example" ".\runs\$pair\.env"
    }
}
```

**Bash / Linux**

```bash
(
    for pair in erp crm; do
        mkdir -p "./runs/$pair" || exit 2
        if [[ ! -e "./runs/$pair/.env" ]]; then
            (umask 077; cp "./examples/batch/$pair.env.example" "./runs/$pair/.env") || exit 2
        fi
        chmod 600 "./runs/$pair/.env" || exit 2
    done
)
```

บน Linux ให้แก้ DSN ในแต่ละ config ให้ตรงกับวิธี authentication ที่ใช้; จากนั้นใช้ [Bash batch](#bash-batch-บน-linux) ด้านล่างแทน `run.ps1`

แก้ `runs/erp/.env` และ `runs/crm/.env` ให้เป็น connection จริงของแต่ละคู่ ตัวอย่างปิด migration ไว้ หากต้องการสร้าง SQL ให้ตั้ง `MSSQL_GENERATE_MIGRATION=true` และคง `MSSQL_MIGRATION_OUT='migration.sql'` เป็น relative path เพื่อแยกไฟล์แต่ละคู่

```text
runs/
├── erp/
│   └── .env       # ERP_Dev → ERP_Prod
└── crm/
    └── .env       # CRM_Dev → CRM_Prod
```

เพิ่มคู่ใหม่ได้โดยสร้างโฟลเดอร์พร้อม `.env` ใต้ `runs` สคริปต์จะอ่านทุกโฟลเดอร์ย่อยโดยเรียงชื่อ ไม่อ่าน `.env` ของ root และไม่ค้นหาโฟลเดอร์ซ้อนหลายระดับ

### Batch online compare

```powershell
powershell.exe -NoProfile -File .\examples\batch\run.ps1
$LASTEXITCODE
Start-Process .\runs\erp\diff.html
```

ใช้ `pwsh` แทน `powershell.exe` ได้ สคริปต์ตั้ง `-sql-mode normalized -format html -timeout 3m` ต่อคู่ เขียน `diff.html` ในโฟลเดอร์ของคู่นั้น และแสดงสรุปพร้อมบันทึก `runs/summary.csv` ที่มี `Pair`, `Status`, `ExitCode`, `Report` โดยไม่บันทึก DSN

- `Equal` / code `0`: ไม่พบความต่าง
- `Changed` / code `1`: พบความต่าง ไม่ใช่การรันล้มเหลว
- `Error`: รันไม่สำเร็จหรือไม่มี `.env`; ยังรันคู่ถัดไป ช่อง `Report` ว่างเพื่อไม่ชี้ไปยังรายงานเก่าที่อาจค้างอยู่
- Exit code รวมเป็น `2` หากมี error, มิฉะนั้นเป็น `1` หากมีความต่างอย่างน้อยหนึ่งคู่ หรือ `0` หากทุกคู่เท่ากัน ไม่มีคู่ให้รันถือเป็น error

สคริปต์ล้าง environment variables ทั้งสี่ `MSSQL_SOURCE_DSN`, `MSSQL_DESTINATION_DSN`, `MSSQL_GENERATE_MIGRATION`, `MSSQL_MIGRATION_OUT` **เฉพาะใน child process** เพื่อให้แต่ละคู่ใช้ `.env` ของตัวเอง ไม่รับ credentials หรือ migration settings ที่ค้างจาก shell และไม่เปลี่ยน environment/current directory ของผู้เรียก

เปลี่ยนตำแหน่ง executable หรือโฟลเดอร์คู่เปรียบเทียบได้:

```powershell
powershell.exe -NoProfile -File .\examples\batch\run.ps1 `
    -Executable 'D:\tools\dbcp.exe' `
    -PairsDirectory 'D:\private\db-pairs'
```

`/runs/` ถูก ignore ทั้งโฟลเดอร์เพื่อป้องกัน credentials, SQL definitions และ migration scripts หลุดเข้า Git หากใช้ path อื่นต้องกำหนดการป้องกันเอง การรันซ้ำใช้ชื่อไฟล์เดิม; หากคู่ใด error หรือปิด migration ไฟล์เก่าอาจยังอยู่ ให้ดูสถานะรอบล่าสุดก่อนใช้ผลลัพธ์

Batch ไม่ execute migration และไม่จัด dependency ข้ามฐานข้อมูล ต้องตรวจแต่ละ script และเลือก destination ให้ถูกต้องก่อนรันเอง

### Batch snapshot

ใช้ config แต่ละโฟลเดอร์ใน `runs` ตามขั้นตอน [เตรียม config](#เตรียม-config-แยกต่อฐานข้อมูล) โดยตั้ง source เป็นฐานข้อมูลที่ต้องการติดตาม และปิด migration ทุกโฟลเดอร์:

```powershell
powershell.exe -NoProfile -File .\examples\batch\run.ps1 -Snapshot
```

สคริปต์ส่ง history root แบบ absolute path ไปยัง CLI จึงรวมประวัติที่ `<project>/data/ddmmyyhhmmss/<database-id>/` ไม่กระจายไปอยู่ใต้ `runs/<คู่>/data` แต่ละฐานข้อมูลใช้เวลาที่ capture ของตัวเอง เลือก root อื่นได้ด้วย `-DataDirectory 'D:\private\schema-history'`

สำเนารายงานล่าสุดยังอยู่ที่ `runs/<คู่>/diff.html` และมี `runs/summary.csv` เหมือน batch ปกติ สถานะ `Saved` หมายถึงเก็บ baseline หรือไม่พบความต่าง, `Changed` หมายถึงมีความต่าง, `Error` หมายถึงรันไม่สำเร็จ หากหลาย config ชี้ source เดียวกันจะใช้ history เดียวกัน

### Batch export สำหรับ migration

สำหรับ export ที่จะนำไปสร้าง migration แบบ offline ใช้:

```powershell
powershell.exe -NoProfile -File .\examples\batch\run.ps1 -Snapshot -IncludeMigration
```

`-IncludeMigration` ต้องใช้คู่กับ `-Snapshot` มิฉะนั้น runner จะ error ก่อนรัน child process เมื่อเปิด switch นี้จะส่ง `-include-migration` ให้ CLI ของแต่ละฐานข้อมูล โดยยังใช้ history root กลางเดิมและยังต้องตั้ง `MSSQL_GENERATE_MIGRATION=false` ทุก config ไม่สร้างหรือ execute migration SQL ระหว่าง batch export

### Bash batch บน Linux

ตัวอย่างนี้รันทีละ config จาก `runs/*/` โดยใช้ binary บน Linux และ `.env` ของแต่ละโฟลเดอร์ ไม่ต้องติดตั้ง PowerShell ใช้ Bash และ GNU `realpath` แสดง exit code ต่อคู่กับผลรวมใน terminal **ไม่สร้าง `summary.csv` แบบ PowerShell runner**

รันจาก root ของโปรเจกต์ ค่าเริ่มต้นเป็น online compare; สำหรับ snapshot ให้เปลี่ยนบรรทัด `args=(...)` ตามตัวอย่างหลัง code block:

```bash
batch_exit=0
(
    exe="$(realpath ./dbcp)" || exit 2
    args=(-sql-mode normalized -format html -out diff.html -timeout 3m)
    shopt -s nullglob
    pairs=(./runs/*/)
    if (( ${#pairs[@]} == 0 )); then
        printf 'No pair directories found.\n' >&2
        exit 2
    fi

    result=0
    for pair in "${pairs[@]}"; do
        code=0
        (
            cd "$pair" || exit 2
            if [[ ! -f .env ]]; then
                printf 'Missing .env: %s\n' "$pair" >&2
                exit 2
            fi
            env -u MSSQL_SOURCE_DSN -u MSSQL_DESTINATION_DSN \
                -u MSSQL_GENERATE_MIGRATION -u MSSQL_MIGRATION_OUT \
                "$exe" "${args[@]}" > /dev/null
        ) || code=$?
        printf '%s: exit %s\n' "$pair" "$code"
        case "$code" in
            0) ;;
            1) if [[ $result == 0 ]]; then result=1; fi ;;
            *) result=2 ;;
        esac
    done
    exit "$result"
) || batch_exit=$?
printf 'Batch exit code: %s\n' "$batch_exit"
```

สำหรับ **snapshot history** ให้แทน `args=(...)` ภายใน subshell ก่อน loop ด้วยสองบรรทัดนี้ เพื่อใช้ history root กลางแบบ absolute path และตั้ง `MSSQL_GENERATE_MIGRATION=false` ในทุก `.env`:

```bash
data_root="$(realpath -m ./data)" || exit 2
args=(-snapshot -data-dir "$data_root" -sql-mode normalized -format html -out diff.html -timeout 3m)
```

สำหรับ **export พร้อม migration metadata** เพิ่ม `-include-migration` หลังบรรทัดกำหนด array ข้างต้น:

```bash
args+=(-include-migration)
```

Exit code รวมใช้ `2` เมื่อมี error, มิฉะนั้นใช้ `1` เมื่อมีความต่าง หรือ `0` เมื่อทุก config สำเร็จโดยไม่ต่าง/เก็บ baseline ใหม่ การรับ code ผ่าน `||` ทำให้รันคู่ถัดไปได้แม้เปิด `set -e` หากนำไปใช้ใน CI ให้จบ script ด้วย `exit "$batch_exit"` เพื่อส่งผลรวมกลับ ห้ามใช้รายงานหรือ SQL เก่าของคู่ที่ error

## Reference

- [สิ่งที่รองรับ](#สิ่งที่รองรับ) · [SQL comparison modes](#sql-comparison-modes) · [CLR function comparison](#clr-function-comparison)
- [รายงานตัวอย่างและการอ่าน diff](#รายงานตัวอย่าง)
- [Configuration](#configuration) · [Environment / .env](#environment--env) · [CLI](#cli)
- [สิทธิ์ฐานข้อมูล](#สิทธิ์ฐานข้อมูล) · [Exit codes](#exit-codes) · [ข้อจำกัดและความปลอดภัย](#ข้อจำกัดและความปลอดภัย)

### สิ่งที่รองรับ

| Object | ข้อมูลที่เปรียบเทียบ |
| --- | --- |
| User tables | ชื่อตาราง ชื่อคอลัมน์ ชนิดข้อมูล และ nullability |
| Primary keys | คอลัมน์ ลำดับคอลัมน์ ทิศทาง ASC/DESC และ clustered/nonclustered |
| SQL stored procedures | Definition, `ANSI_NULLS`, `QUOTED_IDENTIFIER` |
| Views | Definition, `ANSI_NULLS`, `QUOTED_IDENTIFIER` |
| SQL functions | Scalar, inline TVF และ multi-statement TVF พร้อม definition และ session settings ข้างต้น |
| CLR functions (`FS`/`FT`) | Assembly identity/permission set/DLL SHA-256, class/method, signature และ execution settings |
| Synonyms | ชื่อ target ที่อ้างอิง |

SQL definition ที่เปลี่ยนจะแสดงเป็น unified diff พร้อม context 3 บรรทัด รายงาน HTML เป็นไฟล์ standalone เปิดแบบ offline ได้

รายงาน HTML จัด metadata เป็นแถวเปรียบเทียบ Source/Destination และแสดง SQL diff แบบเต็ม ลดช่องว่างให้กระชับโดยไม่ซ่อนรายละเอียด พร้อม layout สำหรับจอขนาดเล็กและการพิมพ์

#### SQL comparison modes

ทั้งสองโหมดปรับ line endings จาก CRLF เป็น LF ก่อนเปรียบเทียบ:

- **`strict`**: เปรียบเทียบข้อความที่เหลือทั้งหมด
- **`normalized`**: เพิ่มการทำให้ declaration `CREATE`, `ALTER`, `CREATE OR ALTER` และ spacing ก่อนชนิด module อยู่ในรูปแบบเดียวกัน

`normalized` ไม่ใช่ semantic SQL comparison: comments, literals, identifier case และ formatting ส่วนอื่นยังมีผลต่อความต่าง ส่วน diff แสดง definition เดิม จึงอาจเห็น declaration ที่ถูกละเว้นในการเปรียบเทียบอยู่ใน hunk เมื่อมีส่วนอื่นเปลี่ยนด้วย

#### CLR function comparison

รองรับ CLR scalar functions (`FS`) และ CLR table-valued functions (`FT`) โดยอ่าน metadata ไม่เรียกใช้ function และไม่พยายามหา T-SQL definition ที่ CLR ไม่มี:

- ชื่อและ identity ของ assembly รวมถึง permission set
- SHA-256 ของ DLL หลัก (`sys.assembly_files.file_id = 1`) เพื่อจับ binary ที่เปลี่ยนแม้ชื่อและ version เดิม; อ่านและ hash ครั้งเดียวต่อ assembly ในแต่ละฐานข้อมูล
- Class และ method ที่ function ผูกไว้
- Signature: ชื่อ/ลำดับ/ชนิดข้อมูลของ parameters, default values, scalar return type หรือชื่อ/ลำดับ/ชนิดข้อมูล/nullability/collation ของ return columns สำหรับ TVF
- `EXECUTE AS` โดยเปรียบเทียบชื่อ principal ไม่ใช่ database-local ID และ `NULL ON NULL INPUT`

ผลต่างใช้หมวด `CLR_FUNCTION_*` ในรายงาน Text/HTML เช่น `CLR_FUNCTION_ASSEMBLY_SHA256` และ `CLR_FUNCTION_SIGNATURE` การตั้ง `strict` หรือ `normalized` ไม่เปลี่ยนวิธีเปรียบเทียบ CLR

ถ้าอ่าน binding, signature หรือ DLL bytes ไม่ได้ โปรแกรมจะหยุดแทนการข้าม object ทั้งนี้ยังไม่เปรียบเทียบ dependency assemblies, ไฟล์ ancillary เช่น source/debug symbols หรือ instance-level CLR settings และยังไม่รองรับ CLR procedures (`PC`), CLR aggregates (`AF`) และ extended procedures (`X`)

### รายงานตัวอย่าง

ตัวอย่างเหล่านี้สร้างผ่าน CLI จริงด้วย `-sql-mode normalized` จาก schema สมมติ ไม่มีข้อมูลหรือ connection credentials ของระบบจริง และเปิดดูได้โดยไม่ต้องเชื่อมต่อ SQL Server:

- [รายงานข้อความ — examples/report.txt](examples/report.txt): อ่านใน GitHub หรือ text editor ได้ทันที
- [รายงาน HTML — examples/report.html](examples/report.html): ดาวน์โหลดหรือ clone แล้วเปิดด้วย browser; ไม่ต้องมี web server หรืออินเทอร์เน็ต GitHub จะแสดง source ของไฟล์ ไม่ใช่หน้า report ที่ render แล้ว

```powershell
Start-Process .\examples\report.html
```

**Bash / Linux desktop**

```bash
xdg-open ./examples/report.html
```

ตัวอย่างมี **12 differences** ครอบคลุมตารางและ stored procedure ที่มีเฉพาะฝั่งใดฝั่งหนึ่ง, ชนิดข้อมูล/nullability/คอลัมน์ที่ขาด, primary key, SQL definition ของ procedure/view/function และ target ของ synonym ส่วนตาราง `dbo.Orders` เหมือนกันทั้งสองฝั่ง จึงไม่ปรากฏเป็นรายการความต่าง

ใน SQL unified diff บรรทัด `-` คือข้อความฝั่ง **source** และ `+` คือข้อความฝั่ง **destination** ตามหัวข้อ `--- source` / `+++ destination` รายงานนี้ไม่ใช่ migration script และไม่ควรนำไปรันเป็น SQL

### Configuration

#### Environment / `.env`

ค่ากลุ่มนี้ใช้เฉพาะโหมดที่อ่าน SQL Server (`-snapshot` หรือเทียบสองฐานข้อมูลโดยตรง) โหมด `-source-snapshot` คู่กับ `-destination-snapshot` ไม่อ่าน `.env` และไม่ใช้ค่า database/migration จาก environment

| ตัวแปร | ค่าเริ่มต้น | ความหมาย |
| --- | --- | --- |
| `MSSQL_SOURCE_DSN` | ไม่มี; จำเป็นเมื่ออ่าน SQL Server | Connection string ของ source; ไม่ใช้ในโหมด offline |
| `MSSQL_DESTINATION_DSN` | ไม่มี; จำเป็นในโหมดเทียบสองฐานข้อมูล | Connection string ของ destination; ไม่ใช้ใน `-snapshot` หรือ offline |
| `MSSQL_GENERATE_MIGRATION` | `false` | ใช้ `true` เพื่อสร้าง migration SQL เมื่อเทียบสองฐานข้อมูลโดยตรง; `-snapshot` ต้องเป็น `false`, offline ไม่ใช้ค่านี้ |
| `MSSQL_MIGRATION_OUT` | `migration.sql` | Path ของไฟล์ migration ในโหมด online แยกจาก path รายงาน; offline ใช้ `-migration-out` แทน |

เมื่อปิด migration generation ในโหมด online โปรแกรมจะไม่สร้างหรือแก้ไขไฟล์ migration เดิม Environment variables สามารถใช้ override ค่าจาก `.env` ได้ เช่น ปิด migration สำหรับการรันใน PowerShell session ปัจจุบัน:

```powershell
$env:MSSQL_GENERATE_MIGRATION = 'false'
.\dbcp.exe
Remove-Item Env:MSSQL_GENERATE_MIGRATION
```

**Bash / Linux** — override เฉพาะคำสั่ง ไม่เปลี่ยน environment ของ shell:

```bash
MSSQL_GENERATE_MIGRATION=false ./dbcp
```

#### CLI

| Option | ค่าเริ่มต้น | ใช้กับ | ความหมาย |
| --- | --- | --- | --- |
| `-format text\|html` | `text` | ทุกโหมด | รูปแบบรายงาน |
| `-out <path>` | ไม่เขียนไฟล์ | ทุกโหมด | บันทึกรายงานเพิ่มเติมจาก stdout; เขียนทับไฟล์เดิมเมื่อสำเร็จ; offline ห้ามทับ snapshot input |
| `-sql-mode strict\|normalized` | `strict` | ทุกโหมด | วิธีเปรียบเทียบ SQL definition |
| `-timeout <duration>` | `1m` | Online / Snapshot | เวลารวมสำหรับเชื่อมต่อและอ่าน schema เช่น `30s`, `2m` |
| `-snapshot` | `false` | Snapshot | เทียบ source กับ schema snapshot ก่อนหน้าและเก็บรอบใหม่; ไม่สร้าง migration; ห้ามใช้ร่วมกับ flags เทียบไฟล์ offline |
| `-include-migration` | `false` | Snapshot | ใช้กับ `-snapshot` เท่านั้น เพื่อเก็บ catalog dependencies/safety metadata สำหรับ offline generation; ไม่สร้างหรือ execute SQL |
| `-source-snapshot <file>` | ไม่ใช้ | Offline | ไฟล์ snapshot ฝั่ง source สำหรับ offline; ต้องใช้คู่กับ `-destination-snapshot` |
| `-destination-snapshot <file>` | ไม่ใช้ | Offline | ไฟล์ snapshot ฝั่ง destination สำหรับ offline; ต้องใช้คู่กับ `-source-snapshot`; ห้ามใช้ร่วมกับ `-snapshot` |
| `-migration-out <file>` | ไม่สร้าง SQL | Offline | Opt in การสร้าง migration แบบ offline; ใช้ได้เฉพาะกับ snapshot input ทั้งคู่ที่มี migration metadata ไม่ใช้ config/connection |
| `-data-dir <path>` | `data` | Snapshot | Root ของ snapshot history; ใช้ใน `-snapshot` |
| `-help` | — | ทุกโหมด | แสดงวิธีใช้งาน |

Path แบบ relative อ้างอิงจาก current working directory รูปแบบรายงาน, SQL mode, timeout และ path รายงานยังคงตั้งค่าผ่าน CLI ไม่ใช่ `.env`

### สิทธิ์ฐานข้อมูล

บัญชีที่ใช้เชื่อมต่อต้องมี database-level `VIEW DEFINITION` บนทุกฐานข้อมูลที่อ่าน: Online ใช้ทั้ง source และ destination; Snapshot ใช้เฉพาะ source

หากเปิด online migration generation หรือ export ด้วย `-snapshot -include-migration` ต้องมี `SELECT` บน `sys.sql_expression_dependencies` เพิ่มเติม สำหรับ isolated networks ให้ DBA ให้สิทธิ์บนฐานข้อมูลที่ export ภายในแต่ละเครือข่าย; เครื่อง offline ไม่ต้องมีบัญชีฐานข้อมูล

ตัวอย่างสำหรับ database user ที่มีอยู่แล้ว ให้ DBA รันในแต่ละฐานข้อมูล:

```sql
GRANT VIEW DEFINITION TO [compare_user];

-- จำเป็นเพิ่มเติมเมื่อเปิด online migration generation หรือ -snapshot -include-migration
GRANT SELECT ON OBJECT::sys.sql_expression_dependencies TO [compare_user];
```

Object-level `DENY` อาจทำให้ metadata บางส่วนถูกซ่อน แม้จะมีสิทธิ์ระดับฐานข้อมูลแล้ว จึงควรตรวจสิทธิ์ของบัญชีให้ครบก่อนเชื่อถือผลเปรียบเทียบ

### Exit codes

| Code | ความหมาย |
| --- | --- |
| `0` | ไม่พบความต่างในขอบเขตที่เปรียบเทียบ หรือบันทึก snapshot baseline ครั้งแรก |
| `1` | พบความต่าง; ไม่ได้หมายความว่าโปรแกรมทำงานผิดพลาด |
| `2` | เกิดข้อผิดพลาด เช่น config, connection, อ่าน metadata/snapshot หรือสร้าง migration ไม่สำเร็จ |

การสร้าง migration สำเร็จยังคืนค่า `1` หากพบความต่าง เพราะโปรแกรมไม่ได้ execute script และไม่ได้เปรียบเทียบซ้ำหลังการแก้ไขฐานข้อมูล

ตัวอย่างอ่าน exit code ใน PowerShell:

```powershell
.\dbcp.exe -format html -out diff.html
$LASTEXITCODE
```

ตัวอย่าง Bash ที่รับ `1` ได้แม้เปิด `set -e` และเก็บ code ก่อนคำสั่งอื่นจะเปลี่ยน `$?`:

```bash
compare_exit=0
./dbcp -format html -out diff.html || compare_exit=$?
case "$compare_exit" in
    0) printf 'No differences.\n' ;;
    1) printf 'Differences found; comparison succeeded.\n' ;;
    *) printf 'Comparison failed (exit %s); do not use stale outputs.\n' "$compare_exit" >&2 ;;
esac
```

หากต้องการส่งสถานะกลับไปยัง CI ให้ใช้ `exit "$compare_exit"` ท้าย script; อย่าใช้ `command && xdg-open ...` หากต้องการเปิดรายงานในกรณีที่พบความต่างด้วย

### ข้อจำกัดและความปลอดภัย

- เปรียบเทียบชื่อแบบ case-sensitive แม้ database collation จะเป็น case-insensitive
- ไม่เปรียบเทียบ row data, ลำดับคอลัมน์ในตาราง, ชื่อ PK constraints, table defaults/identity/computed expressions/collation, non-PK indexes, FK/CHECK/UNIQUE constraints, triggers, object GRANT/DENY permissions และ type/XML schema definitions
- Encrypted/unreadable SQL definitions หรือ CLR metadata และ object ประเภท `PC`/`AF`/`X` ทำให้โปรแกรมแจ้ง error ไม่ใช่ข้ามแล้วรายงานว่าเท่ากัน
- Source และ destination ถูกอ่านตามลำดับ ไม่ได้อยู่ใน snapshot ร่วมกัน ควรหลีกเลี่ยง concurrent DDL ระหว่างเปรียบเทียบ และตรวจความเปลี่ยนแปลงอีกครั้งก่อนนำ migration ไปใช้
- โปรแกรมไม่ใส่ connection credentials ลงในรายงาน แต่ SQL definitions อาจมีความลับฝังอยู่ ต้องปกป้องทั้งรายงานและ migration script
- ไฟล์ผลลัพธ์ชื่อมาตรฐาน `diff.txt`, `diff.html`, `migration.sql` ที่ root ถูก ignore แล้ว หากใช้ชื่อหรือ path อื่น ต้องจัดการ `.gitignore` และสิทธิ์ไฟล์ให้เหมาะสมเอง

## Development

```powershell
go test ./...
go vet ./...
go build -o dbcp.exe .
```

**Bash / Linux**

```bash
go test ./...
go vet ./...
go build -o dbcp .
```

Unit tests ไม่ได้แทนการทดลอง migration บน SQL Server จริง ควรทดสอบ dependency ordering, rollback และเปรียบเทียบซ้ำหลัง apply กับฐานข้อมูลทดลองก่อนใช้ script ใน production
