#requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Executable,
    [string]$PairsDirectory,
    [switch]$Snapshot,
    [string]$DataDirectory,
    [switch]$IncludeMigration
)

$ErrorActionPreference = 'Stop'
$batchExitCode = 0
$results = @()

try {
    if ($IncludeMigration -and -not $Snapshot) {
        throw '-IncludeMigration requires -Snapshot.'
    }
    if (-not $PSBoundParameters.ContainsKey('Executable')) {
        $Executable = Join-Path $PSScriptRoot '../../mssql-batch-compare.exe'
    }
    if (-not $PSBoundParameters.ContainsKey('PairsDirectory')) {
        $PairsDirectory = Join-Path $PSScriptRoot '../../runs'
    }
    $Executable = (Resolve-Path -LiteralPath $Executable).ProviderPath
    $PairsDirectory = (Resolve-Path -LiteralPath $PairsDirectory).ProviderPath
    if ($Snapshot) {
        if (-not $PSBoundParameters.ContainsKey('DataDirectory')) {
            $DataDirectory = Join-Path $PSScriptRoot '../../data'
        }
        $DataDirectory = [System.IO.Path]::GetFullPath($DataDirectory)
        if ($DataDirectory.Contains('"')) {
            throw 'DataDirectory must not contain a double quote.'
        }
        # Windows argv quoting: double trailing backslashes before the closing quote.
        $quotedDataDirectory = '"' + ($DataDirectory -replace '(\\+)$', '$1$1') + '"'
    }
    $pairs = @(Get-ChildItem -LiteralPath $PairsDirectory -Directory | Sort-Object Name)
    if ($pairs.Count -eq 0) {
        throw 'No pair directories found. Create a directory with a .env file for each pair.'
    }

    foreach ($pair in $pairs) {
        Write-Host "Comparing: $($pair.Name)"
        $code = 2
        $process = $null
        try {
            if (-not (Test-Path -LiteralPath (Join-Path $pair.FullName '.env') -PathType Leaf)) {
                throw 'Missing .env file; this pair was not run.'
            }

            $start = New-Object System.Diagnostics.ProcessStartInfo
            $start.FileName = $Executable
            $start.WorkingDirectory = $pair.FullName
            $start.Arguments = '-sql-mode normalized -format html -out diff.html -timeout 3m'
            if ($Snapshot) {
                $start.Arguments += " -snapshot -data-dir $quotedDataDirectory"
                if ($IncludeMigration) {
                    $start.Arguments += ' -include-migration'
                }
            }
            $start.UseShellExecute = $false
            $start.RedirectStandardOutput = $true

            # Each child reads only its own .env for these settings. Do not alter
            # the caller's environment or inherit another pair's credentials.
            foreach ($key in @('MSSQL_SOURCE_DSN', 'MSSQL_DESTINATION_DSN',
                    'MSSQL_GENERATE_MIGRATION', 'MSSQL_MIGRATION_OUT')) {
                $start.EnvironmentVariables.Remove($key)
            }

            $process = New-Object System.Diagnostics.Process
            $process.StartInfo = $start
            [void]$process.Start()
            # -out writes the report. Discard the duplicate stdout as a stream;
            # leave stderr attached so connection/configuration errors are visible.
            $process.StandardOutput.BaseStream.CopyTo([System.IO.Stream]::Null)
            $process.WaitForExit()
            $code = $process.ExitCode
        }
        catch {
            Write-Warning "$($pair.Name): $($_.Exception.Message)"
        }
        finally {
            if ($null -ne $process) {
                $process.Dispose()
            }
        }

        $status = switch ($code) {
            0 { if ($Snapshot) { 'Saved' } else { 'Equal' } }
            1 { 'Changed' }
            default { 'Error' }
        }
        if ($code -notin @(0, 1)) {
            $batchExitCode = 2
        }
        elseif ($code -eq 1 -and $batchExitCode -eq 0) {
            $batchExitCode = 1
        }
        $results += [pscustomobject]@{
            Pair = $pair.Name
            Status = $status
            ExitCode = $code
            Report = if ($code -in @(0, 1)) { Join-Path $pair.FullName 'diff.html' } else { '' }
        }
    }

    $results | Format-Table Pair, Status, ExitCode, Report -AutoSize | Out-Host
    $summary = Join-Path $PairsDirectory 'summary.csv'
    $results | Export-Csv -LiteralPath $summary -NoTypeInformation -Encoding UTF8
    Write-Host "Summary: $summary"
}
catch {
    Write-Warning $_.Exception.Message
    $batchExitCode = 2
}

exit $batchExitCode
