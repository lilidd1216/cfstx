; CFSTX Windows 安装包脚本
; 编译：ISCC.exe installer.iss

#define MyAppName "CFSTX"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "liliaic"
#define MyAppURL "https://gitee.com/liliaic/cfstx"
#define MyAppExeName "cfstx.exe"

[Setup]
AppId={{8A7F3C21-5B4E-4D9A-9F2C-1E6D8B3A5C70}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
; 默认安装到当前用户目录，免管理员权限；用户可在向导中自定义路径
DefaultDirName={localappdata}\Programs\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
; 允许用户选择「仅为我安装」或「为所有用户安装」
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputDir=dist
OutputBaseFilename=CFSTX-Setup-{#MyAppVersion}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesInstallIn64BitMode=x64compatible
; 覆盖安装时自动关闭正在运行的旧版本，安装完成后重新启动
CloseApplications=yes
RestartApplications=yes
UninstallDisplayIcon={app}\{#MyAppExeName}
MinVersion=10.0

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Additional tasks:"; Flags: unchecked

[Files]
Source: "build\bin\cfstx.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "ip.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "ipv6.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "version.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "script\*"; DestDir: "{app}\script"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "Run {#MyAppName}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}"
