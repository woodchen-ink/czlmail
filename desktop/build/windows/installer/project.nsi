Unicode true

####
## Please note: Template replacements don't work in this file. They are provided with default defines like
## mentioned underneath.
## If the keyword is not defined, "wails_tools.nsh" will populate them with the values from ProjectInfo.
## If they are defined here, "wails_tools.nsh" will not touch them. This allows to use this project.nsi manually
## from outside of Wails for debugging and development of the installer.
##
## For development first make a wails nsis build to populate the "wails_tools.nsh":
## > wails build --target windows/amd64 --nsis
## Then you can call makensis on this file with specifying the path to your binary:
## For a AMD64 only installer:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app.exe
## For a ARM64 only installer:
## > makensis -DARG_WAILS_ARM64_BINARY=..\..\bin\app.exe
## For a installer with both architectures:
## > makensis -DARG_WAILS_AMD64_BINARY=..\..\bin\app-amd64.exe -DARG_WAILS_ARM64_BINARY=..\..\bin\app-arm64.exe
####
## The following information is taken from the ProjectInfo file, but they can be overwritten here.
####
## !define INFO_PROJECTNAME    "MyProject" # Default "{{.Name}}"
## !define INFO_COMPANYNAME    "MyCompany" # Default "{{.Info.CompanyName}}"
## !define INFO_PRODUCTNAME    "MyProduct" # Default "{{.Info.ProductName}}"
## !define INFO_PRODUCTVERSION "1.0.0"     # Default "{{.Info.ProductVersion}}"
## !define INFO_COPYRIGHT      "Copyright" # Default "{{.Info.Copyright}}"
###
## !define PRODUCT_EXECUTABLE  "Application.exe"      # Default "${INFO_PROJECTNAME}.exe"
## !define UNINST_KEY_NAME     "UninstKeyInRegistry"  # Default "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
####
# Per-user install under %LOCALAPPDATA%\CZL: the install dir stays writable,
# so attachments live in <install>\attachments and self-update needs no UAC.
!define REQUEST_EXECUTION_LEVEL "user"
## !define REQUEST_EXECUTION_LEVEL "admin"            # Default "admin"  see also https://nsis.sourceforge.io/Docs/Chapter4.html
####
## Include the wails tools
####
!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!insertmacro MUI_PAGE_FINISH # Finished installation page.

# Uninstall options: an unchecked "also delete all data" component.
!define MUI_COMPONENTSPAGE_NODESC
!insertmacro MUI_UNPAGE_COMPONENTS
!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "English" # Set the Language of the installer

## The following two statements can be used to sign the installer and the uninstaller. The path to the binaries are provided in %1
#!uninstfinalize 'signtool --file "%1"'
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
InstallDir "$LOCALAPPDATA\CZL\${INFO_PRODUCTNAME}"
ShowInstDetails show # This will always show the installation details.

# Silent installs are started by the in-app updater, which has already quit; reopen the app.
# czl.stopRunning below must not use taskkill /T: the updater is the parent of this installer.
Function .onInstSuccess
    IfSilent 0 +2
    Exec '"$INSTDIR\${PRODUCT_EXECUTABLE}"'
FunctionEnd

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

# The app keeps running in the tray after its window closes; stop it before overwriting the exe.
!macro czl.stopRunning
    nsExec::Exec 'taskkill /F /IM "${PRODUCT_EXECUTABLE}"'
    Sleep 500
!macroend

# Versions before the move installed to %LOCALAPPDATA%\Programs\CZL Mail. Carry the opened
# attachments / drive files over, repoint autostart at the new exe, then remove the old copy.
!macro czl.migrateOldInstall
    StrCpy $0 "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
    ${If} $0 != $INSTDIR
    ${AndIf} ${FileExists} "$0\${PRODUCT_EXECUTABLE}"
        ${IfNot} ${FileExists} "$INSTDIR\attachments\*.*"
            Rename "$0\attachments" "$INSTDIR\attachments"
        ${EndIf}
        ${IfNot} ${FileExists} "$INSTDIR\files\*.*"
            Rename "$0\files" "$INSTDIR\files"
        ${EndIf}
        ReadRegStr $1 HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${INFO_PRODUCTNAME}"
        ${If} $1 != ""
            WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${INFO_PRODUCTNAME}" '"$INSTDIR\${PRODUCT_EXECUTABLE}" --background'
        ${EndIf}
        RMDir /r "$0"
    ${EndIf}
!macroend

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    !insertmacro czl.stopRunning

    SetOutPath $INSTDIR

    !insertmacro wails.files

    !insertmacro czl.migrateOldInstall

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

# Optional, unchecked by default. Runs before the main section so the exe still exists:
# `czlmail.exe purge` deletes the cache, settings, logs, downloaded attachments, keychain
# credentials, autostart and protocol registrations. Server-side data is untouched.
Section /o "un.同时删除所有数据（邮件缓存、设置、登录凭据）" SecPurge
    !insertmacro czl.stopRunning
    ExecWait '"$INSTDIR\${PRODUCT_EXECUTABLE}" purge'
SectionEnd

Section "-un.main"
    !insertmacro wails.setShellContext

    !insertmacro czl.stopRunning

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd
