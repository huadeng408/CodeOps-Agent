function Resolve-LoopbackHttpEndpoint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Url
    )

    $endpoint = $null
    if (-not [Uri]::TryCreate($Url, [UriKind]::Absolute, [ref]$endpoint)) {
        throw "ServerUrl must be an absolute HTTP URL."
    }
    if ($endpoint.Scheme -ne [Uri]::UriSchemeHttp) {
        throw "ServerUrl must use HTTP for the local server."
    }
    if (-not $endpoint.IsLoopback) {
        throw "ServerUrl must target a loopback address."
    }
    if (
        -not [string]::IsNullOrEmpty($endpoint.UserInfo) -or
        -not [string]::IsNullOrEmpty($endpoint.Query) -or
        -not [string]::IsNullOrEmpty($endpoint.Fragment) -or
        $endpoint.AbsolutePath -ne "/"
    ) {
        throw "ServerUrl must contain only a loopback origin."
    }

    return [pscustomobject]@{
        Url = $endpoint.GetLeftPart([UriPartial]::Authority)
        Port = $endpoint.Port
    }
}

function Resolve-OtlpHttpTraceEndpoint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$PhoenixUrl
    )

    $endpoint = $null
    if (-not [Uri]::TryCreate($PhoenixUrl, [UriKind]::Absolute, [ref]$endpoint)) {
        throw "PhoenixUrl must be an absolute HTTP(S) URL."
    }
    if ($endpoint.Scheme -notin @([Uri]::UriSchemeHttp, [Uri]::UriSchemeHttps)) {
        throw "PhoenixUrl must use HTTP or HTTPS."
    }
    if (
        -not [string]::IsNullOrEmpty($endpoint.UserInfo) -or
        -not [string]::IsNullOrEmpty($endpoint.Query) -or
        -not [string]::IsNullOrEmpty($endpoint.Fragment) -or
        $endpoint.AbsolutePath -ne "/"
    ) {
        throw "PhoenixUrl must contain only an HTTP(S) origin."
    }

    return $endpoint.GetLeftPart([UriPartial]::Authority) + "/v1/traces"
}
