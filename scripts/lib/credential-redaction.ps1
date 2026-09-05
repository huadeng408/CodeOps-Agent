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
        # Start-Process may host this helper without a console (notably on
        # POSIX CI). Write directly to the redirected stream and flush each
        # record so a parent can terminate the wrapper without losing the
        # child's final diagnostic lines.
        [Console]::Out.WriteLine((Protect-CredentialText -Text $_ -Secrets $Secrets))
        [Console]::Out.Flush()
    }
    $ExitCode.Value = $LASTEXITCODE
}
