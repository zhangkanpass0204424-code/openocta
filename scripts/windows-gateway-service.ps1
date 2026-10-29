param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet("Build", "Install", "Start", "Stop", "Restart", "Status", "Uninstall")]
    [string]$Action,

    [string]$NssmPath,

    [string]$ServiceName = "OpenOctaGateway",

    [string]$ServiceAccountName = "$env:USERDOMAIN\$env:USERNAME",

    [string]$StateDir = (Join-Path $env:APPDATA "openocta")
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$uiDir = Join-Path $repoRoot "ui"
$srcDir = Join-Path $repoRoot "src"
$embedDir = Join-Path $srcDir "embed"
$binaryPath = Join-Path $repoRoot "openocta.exe"
$stateDir = $StateDir
$logDir = Join-Path $stateDir "logs"

function Invoke-External {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Executable,

        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$Arguments
    )

    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "'$Executable $($Arguments -join ' ')' failed with exit code $LASTEXITCODE."
    }
}

function Resolve-Nssm {
    if ($NssmPath) {
        $resolved = (Resolve-Path $NssmPath).Path
        if ((Get-Item $resolved).PSIsContainer) {
            throw "NssmPath must point to nssm.exe, not a directory."
        }
        return $resolved
    }

    $command = Get-Command "nssm.exe" -ErrorAction SilentlyContinue
    if (-not $command) {
        throw "NSSM was not found. Install NSSM or pass -NssmPath 'C:\path\to\nssm.exe'."
    }
    return $command.Source
}

function Invoke-Nssm {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    Invoke-External -Executable $script:nssm @Arguments
}

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object -TypeName Security.Principal.WindowsPrincipal -ArgumentList $identity
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "Run this action from an elevated PowerShell window (Run as administrator)."
    }
}

function Set-ServiceAccount {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [string]$Account,

        [Parameter(Mandatory = $true)]
        [IntPtr]$Password
    )

    if (-not ("OpenOctaServiceAccountConfig" -as [type])) {
        Add-Type -TypeDefinition @"
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class OpenOctaServiceAccountConfig
{
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr OpenSCManager(string machineName, string databaseName, uint desiredAccess);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr OpenService(IntPtr manager, string serviceName, uint desiredAccess);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool ChangeServiceConfig(
        IntPtr service, uint serviceType, uint startType, uint errorControl,
        IntPtr binaryPath, IntPtr loadOrderGroup, IntPtr tagId, IntPtr dependencies,
        string account, IntPtr password, IntPtr displayName);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool CloseServiceHandle(IntPtr handle);

    public static void SetAccount(string serviceName, string account, IntPtr password)
    {
        IntPtr manager = OpenSCManager(null, null, 0x0001);
        if (manager == IntPtr.Zero)
            throw new Win32Exception(Marshal.GetLastWin32Error());

        try
        {
            IntPtr service = OpenService(manager, serviceName, 0x0002);
            if (service == IntPtr.Zero)
                throw new Win32Exception(Marshal.GetLastWin32Error());

            try
            {
                if (!ChangeServiceConfig(service, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF,
                    IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero,
                    account, password, IntPtr.Zero))
                    throw new Win32Exception(Marshal.GetLastWin32Error());
            }
            finally
            {
                CloseServiceHandle(service);
            }
        }
        finally
        {
            CloseServiceHandle(manager);
        }
    }
}
"@
    }

    [OpenOctaServiceAccountConfig]::SetAccount($Name, $Account, $Password)
}

function Build-OpenOcta {
    $yarn = (Get-Command "yarn.cmd" -ErrorAction Stop).Source
    $go = (Get-Command "go.exe" -ErrorAction Stop).Source

    Push-Location $uiDir
    try {
        Invoke-External -Executable $yarn -Arguments @("install", "--frozen-lockfile")
        Invoke-External -Executable $yarn -Arguments @("build")
    }
    finally {
        Pop-Location
    }

    $embeddedFrontendDir = Join-Path $embedDir "frontend"
    if (-not (Test-Path (Join-Path $embeddedFrontendDir "index.html"))) {
        throw "Frontend build did not produce '$embeddedFrontendDir\index.html'."
    }

    foreach ($asset in @(".env", "config-schema.json", "openocta.json.example")) {
        $source = Join-Path $srcDir $asset
        if (Test-Path $source) {
            Copy-Item -Path $source -Destination $embedDir -Force
        }
    }

    Push-Location $srcDir
    try {
        Invoke-External -Executable $go -Arguments @("mod", "download")
        Invoke-External -Executable $go -Arguments @("build", "-o", $binaryPath, ".\cmd\openocta")
    }
    finally {
        Pop-Location
    }

    Write-Host "Built $binaryPath"
}

switch ($Action) {
    "Build" {
        Build-OpenOcta
    }
    "Install" {
        Assert-Administrator
        if (-not (Test-Path $binaryPath)) {
            throw "Gateway executable not found. Run this script with the Build action first."
        }

        $script:nssm = Resolve-Nssm
        if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
            throw "Service '$ServiceName' already exists. Uninstall it before installing again."
        }

        if (-not $env:APPDATA) {
            throw "APPDATA is not set for the current user."
        }
        New-Item -ItemType Directory -Path $logDir -Force | Out-Null

        Invoke-Nssm -Arguments @("install", $ServiceName, $binaryPath)
        try {
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppDirectory", $srcDir)
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppParameters", "gateway run --port 18900")
            Invoke-Nssm -Arguments @("set", $ServiceName, "Start", "SERVICE_DEMAND_START")
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppEnvironmentExtra", "OPENOCTA_RUN_MODE=desktop", "OPENOCTA_STATE_DIR=$stateDir", "OPENOCTA_SKIP_SINGLETON_KILL=1")
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppStdout", (Join-Path $logDir "gateway-service-stdout.log"))
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppStderr", (Join-Path $logDir "gateway-service-stderr.log"))
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppRotateFiles", "1")
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppRotateBytes", "10485760")
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppExit", "Default", "Restart")
            Invoke-Nssm -Arguments @("set", $ServiceName, "AppRestartDelay", "5000")

            $credential = Get-Credential -UserName $ServiceAccountName -Message "Enter the password for the Windows account that will run the OpenOcta service."
            if (-not $credential) {
                throw "Service account credentials were not provided."
            }

            $passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToGlobalAllocUnicode($credential.Password)
            try {
                Set-ServiceAccount -Name $ServiceName -Account $credential.UserName -Password $passwordPointer
            }
            finally {
                [Runtime.InteropServices.Marshal]::ZeroFreeGlobalAllocUnicode($passwordPointer)
                $credential = $null
            }

            Write-Host "Installed manual-start service '$ServiceName'. Start it with: .\scripts\windows-gateway-service.ps1 Start"
        }
        catch {
            & $script:nssm remove $ServiceName confirm | Out-Null
            throw
        }
    }
    "Start" {
        Start-Service -Name $ServiceName
    }
    "Stop" {
        Stop-Service -Name $ServiceName
    }
    "Restart" {
        Restart-Service -Name $ServiceName
    }
    "Status" {
        Get-Service -Name $ServiceName | Format-List Name, DisplayName, Status, StartType
    }
    "Uninstall" {
        Assert-Administrator
        $script:nssm = Resolve-Nssm
        $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
        if ($service -and $service.Status -ne "Stopped") {
            Stop-Service -Name $ServiceName
            $service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(30))
        }
        if ($service) {
            Invoke-Nssm -Arguments @("remove", $ServiceName, "confirm")
            Write-Host "Removed service '$ServiceName'."
        }
        else {
            Write-Host "Service '$ServiceName' is not installed."
        }
    }
}
