; NEXUS Windows installer (NSIS 3). Per-user install: no administrator rights,
; which also lets NEXUS update itself silently.
;
;   makensis -DVERSION=0.3.17 -DNUMVERSION=0.3.17.0 installer/nexus.nsi
;
; Command-line options (used by the self-updater):
;   /S          silent
;   /UPDATE     updating an existing install: keep the user's shortcut choices
;   /RELAUNCH   start NEXUS when finished

Unicode true
!include "MUI2.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"
!include "WinVer.nsh"
!include "x64.nsh"

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef NUMVERSION
  !define NUMVERSION "0.0.0.0"
!endif
!ifndef SRCDIR
  !define SRCDIR "..\dist\windows-amd64"
!endif
!ifndef OUTFILE
  !define OUTFILE "..\dist\NEXUS-Setup-${VERSION}.exe"
!endif
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\NEXUS"
!define WEBVIEW2_GUID "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Name "NEXUS"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\NEXUS"
InstallDirRegKey HKCU "Software\NEXUS" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
BrandingText "NEXUS ${VERSION}"
ShowInstDetails nevershow

VIProductVersion "${NUMVERSION}"
VIAddVersionKey "ProductName" "NEXUS"
VIAddVersionKey "FileDescription" "NEXUS Setup"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "CompanyName" "NEXUS"
VIAddVersionKey "LegalCopyright" "NEXUS project"

!define MUI_ICON "assets\nexus.ico"
!define MUI_UNICON "assets\nexus.ico"
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "Install NEXUS ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "NEXUS is an AI-powered command center for your Linux servers.$\r$\n$\r$\nIt installs for your Windows account only (no administrator rights needed) and keeps itself up to date automatically.$\r$\n$\r$\nYour data is stored in %LOCALAPPDATA%\NEXUS and is kept when NEXUS is updated."
!define MUI_FINISHPAGE_RUN "$INSTDIR\NEXUS.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Start NEXUS"
!define MUI_COMPONENTSPAGE_NODESC

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Var IsUpdate
Var Relaunch

Function .onInit
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_ICONSTOP "NEXUS requires Windows 10 or later." /SD IDOK
    Abort
  ${EndIf}
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "NEXUS requires 64-bit Windows." /SD IDOK
    Abort
  ${EndIf}
  StrCpy $IsUpdate 0
  StrCpy $Relaunch 0
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/UPDATE" $R1
  ${IfNot} ${Errors}
    StrCpy $IsUpdate 1
  ${EndIf}
  ClearErrors
  ${GetOptions} $R0 "/RELAUNCH" $R1
  ${IfNot} ${Errors}
    StrCpy $Relaunch 1
  ${EndIf}
FunctionEnd

; NEXUS.exe cannot be replaced while it runs. The updater closes the app
; before starting the installer; this waits for it, asks a running app to
; close after 5 s and, for unattended updates only, forces it after 30 s.
Function WaitForNexusExit
  StrCpy $R2 0
  ${IfNot} ${FileExists} "$INSTDIR\NEXUS.exe"
    Return
  ${EndIf}
  loop:
    ClearErrors
    FileOpen $R3 "$INSTDIR\NEXUS.exe" a
    ${IfNot} ${Errors}
      FileClose $R3
      Return
    ${EndIf}
    ${If} $R2 == 0
    ${AndIf} $IsUpdate == 0
      MessageBox MB_OKCANCEL|MB_ICONINFORMATION "NEXUS is running and will be closed to continue." /SD IDOK IDOK +2
      Abort
      nsExec::Exec 'taskkill /IM NEXUS.exe'
    ${EndIf}
    ${If} $R2 == 10
      nsExec::Exec 'taskkill /IM NEXUS.exe'
    ${EndIf}
    ${If} $IsUpdate == 1
    ${AndIf} $R2 >= 60
      nsExec::Exec 'taskkill /F /IM NEXUS.exe'
      Sleep 1500
      Return
    ${EndIf}
    ${If} $R2 >= 120
      ${If} $IsUpdate == 1
        nsExec::Exec 'taskkill /F /IM NEXUS.exe'
        Sleep 1500
        Return
      ${EndIf}
      MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "NEXUS is still running. Close it, then click Retry." /SD IDCANCEL IDRETRY +2
      Abort
      StrCpy $R2 0
    ${EndIf}
    IntOp $R2 $R2 + 1
    Sleep 500
    Goto loop
FunctionEnd

Function CheckWebView2
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ${If} $IsUpdate == 0
      MessageBox MB_YESNO|MB_ICONINFORMATION "NEXUS uses the Microsoft Edge WebView2 Runtime, which is not installed on this PC.$\r$\n$\r$\nOpen Microsoft's download page now? Without it, NEXUS opens in your web browser instead." /SD IDNO IDNO +2
      ExecShell "open" "https://developer.microsoft.com/microsoft-edge/webview2/#download-section"
    ${EndIf}
  ${EndIf}
FunctionEnd

Section "NEXUS (required)" SecMain
  SectionIn RO
  Call WaitForNexusExit
  SetOutPath "$INSTDIR"
  File "${SRCDIR}\NEXUS.exe"
  File "/oname=README.txt" "assets\README.txt"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortCut "$SMPROGRAMS\NEXUS.lnk" "$INSTDIR\NEXUS.exe" "" "$INSTDIR\NEXUS.exe" 0

  WriteRegStr HKCU "Software\NEXUS" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\NEXUS" "Version" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "NEXUS"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "NEXUS project"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\NEXUS.exe,0"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegStr HKCU "${UNINST_KEY}" "URLInfoAbout" "https://github.com/WSEU790/MoistTux"
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
  Call CheckWebView2
SectionEnd

Section "Desktop shortcut" SecDesktop
  ${If} $IsUpdate == 0
    CreateShortCut "$DESKTOP\NEXUS.lnk" "$INSTDIR\NEXUS.exe" "" "$INSTDIR\NEXUS.exe" 0
  ${EndIf}
SectionEnd

Function .onInstSuccess
  ${If} $Relaunch == 1
    Exec '"$INSTDIR\NEXUS.exe"'
  ${EndIf}
FunctionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /IM NEXUS.exe'
  Sleep 1500
  Delete "$INSTDIR\NEXUS.exe"
  Delete "$INSTDIR\README.txt"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\NEXUS.lnk"
  Delete "$DESKTOP\NEXUS.lnk"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "Software\NEXUS"
  ${IfNot} ${Silent}
    MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "Also delete your NEXUS data?$\r$\n$\r$\nThis removes enrolled servers, history, AI conversations, saved SSH logins and this PC's NEXUS identity ($LOCALAPPDATA\NEXUS). Agents on your servers are not removed." IDNO +2
    RMDir /r "$LOCALAPPDATA\NEXUS"
  ${EndIf}
SectionEnd
