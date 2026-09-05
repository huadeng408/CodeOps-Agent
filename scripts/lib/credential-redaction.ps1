function Protect-CredentialText {
    [CmdletBinding()]
    param(
        [AllowNull()]
        [object]$Text,

        [AllowEmptyCollection()]
        [string[]]$Secrets = @()
    )

    if ($null -eq $Text) {
        return ""
    }

    $protected = [string]$Text
    foreach ($secret in $Secrets) {
        if (-not [string]::IsNullOrWhiteSpace($secret)) {
            $protected = $protected.Replace($secret, "<redacted>")
        }
    }

    $protected = [regex]::Replace(
        $protected,
        '(?i)(\bBearer\s+)[A-Za-z0-9][A-Za-z0-9._~+/=-]{5,}',
        '${1}<redacted>'
    )
    return [regex]::Replace(
        $protected,
        '(?i)\bsk-[A-Za-z0-9][A-Za-z0-9._-]{8,}',
        '<redacted>'
    )
}

function Invoke-RedactedNativeCommand {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [AllowEmptyCollection()]
        [string[]]$ArgumentList = @(),

        [AllowEmptyCollection()]
        [string[]]$Secrets = @(),

        [Parameter(Mandatory = $true)]
        [ref]$ExitCode
    )

    & $FilePath @ArgumentList 2>&1 | ForEach-Object {
        # Keep the record on PowerShell's success-output stream.  On POSIX,
        # this helper can run inside a Start-Process wrapper whose stdout is
        # redirected to a file; writing through [Console]::Out bypasses that
        # redirected pipeline when no console is attached.
        Write-Output (Protect-CredentialText -Text $_ -Secrets $Secrets)
    }
    $ExitCode.Value = $LASTEXITCODE
}
