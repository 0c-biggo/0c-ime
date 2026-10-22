Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "bundle.nsh"

!define ARP_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\0c-Zhuyin"
Name "靈犀注音（0c）"
OutFile "0c-installer.exe"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
SetCompressorDictSize 32
CRCCheck on
ManifestDPIAware true
VIProductVersion "${FILE_VERSION}"
VIAddVersionKey /LANG=1028 "ProductName" "靈犀注音（0c）"
VIAddVersionKey /LANG=1028 "FileDescription" "靈犀注音安裝程式 — ${CHANNEL_LABEL}"
VIAddVersionKey /LANG=1028 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1028 "CompanyName" "${PUBLISHER}"
VIAddVersionKey /LANG=1028 "LegalCopyright" "© ${PUBLISHER}"
VIAddVersionKey /LANG=1028 "ProductVersion" "${VERSION}"
!ifdef SIGN_UNINSTALLER
!uninstfinalize 'sign.cmd "%1"' = 0
!endif
ShowInstDetails hide
ShowUninstDetails hide

!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "安裝靈犀注音（0c）"
!define MUI_WELCOMEPAGE_TEXT "專心把話打完，少為選字停下來。$\r$\n$\r$\n此精靈會安裝或升級靈犀注音 ${VERSION}（${CHANNEL_LABEL}）。個人詞庫會保留。$\r$\n$\r$\n請先完成正在輸入的文字，並以目前桌面使用者的同一個帳號允許系統管理員權限。完成後請登出再登入。"
!insertmacro MUI_PAGE_WELCOME
!define MUI_LICENSEPAGE_TEXT_TOP "請閱讀靈犀注音使用條款。安裝後可在安裝目錄的 TERMS.md 再次查看。"
!insertmacro MUI_PAGE_LICENSE "terms.txt"
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_TITLE "靈犀注音已安裝"
!define MUI_FINISHPAGE_TEXT "請登出再登入，讓所有應用程式載入新版。$\r$\n$\r$\n「靈犀注音」已加入 Windows 語言設定「中文（台灣）」的鍵盤，使用 Win＋空白切換；清單裡沒有時，在「中文（台灣）」新增「靈犀注音」鍵盤。個人詞庫已保留。"
!insertmacro MUI_PAGE_FINISH
!define MUI_UNCONFIRMPAGE_TEXT_TOP "解除安裝會停用靈犀注音，保留個人詞庫及版本備份。請先完成正在輸入的文字，完成後登出再登入。"
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!define MUI_FINISHPAGE_TITLE "靈犀注音已解除安裝"
!define MUI_FINISHPAGE_TEXT "請登出再登入。個人詞庫與版本備份已保留。"
!insertmacro MUI_UNPAGE_FINISH
!insertmacro MUI_LANGUAGE "TradChinese"

!define RECEIPT "$LOCALAPPDATA\Zuin\install-receipt.json"
Var PreviousEntry

; 0c-update 預設輸出 UTF-8，nsExec 會當成系統碼頁（CP950）而在對話框顯示亂碼；要求它改送 UTF-16LE（#138）。
!macro RequestUnicodeOutput
  System::Call 'kernel32::SetEnvironmentVariable(t "ZC_OUTPUT_UTF16", t "1")'
!macroend

; 去掉 0c-update 輸出開頭的 BOM（nsExec 會照原樣留下 U+FEFF）與結尾的換行，對話框才不會多空行。
!macro TidyOutputFunction prefix
Function ${prefix}TidyOutput
  Push $R0
  Push $R1
  IntFmt $R0 "%c" 0xFEFF
  StrCpy $R1 $1 1
  StrCmp $R1 $R0 0 trim
  StrCpy $1 $1 "" 1
  trim:
    StrCpy $R1 $1 1 -1
    StrCmp $R1 "$\n" drop
    StrCmp $R1 "$\r" drop done
  drop:
    StrCpy $1 $1 -1
    Goto trim
  done:
  Pop $R1
  Pop $R0
FunctionEnd
!macroend
!insertmacro TidyOutputFunction ""
!insertmacro TidyOutputFunction "un."

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "此安裝程式需要 Windows x64。" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
  SetRegView 64
  SetShellVarContext current
  StrCpy $INSTDIR "$LOCALAPPDATA\Zuin\setup\${SETUP_ID}"
  !insertmacro RequestUnicodeOutput
  ; 系統管理員權限由另一個帳號確認時（over-the-shoulder），整套安裝會落在那個帳號，桌面使用者用不到、之後也補不上：
  ; 開始安裝前就擋下並說明（#199）。只有 0c-update 確定是另一個帳號時（結束碼 3）才擋；它沒跑成時照常進入精靈，
  ; 安裝步驟會再回報原因。靜默安裝（/S）同樣擋下，以結束碼 1 結束。
  InitPluginsDir
  File "/oname=$PLUGINSDIR\0c-update.exe" "0c-update.exe"
  nsExec::ExecToStack '"$PLUGINSDIR\0c-update.exe" check-account'
  Pop $0
  Pop $1
  ${If} $0 == "3"
    Call TidyOutput
    StrCpy $2 $1 11
    ${If} $2 == "0c-update: "
      StrCpy $1 $1 "" 11
    ${EndIf}
    MessageBox MB_ICONSTOP "$1" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
FunctionEnd

; $1 是 0c-update 的輸出、$0 是結束碼；輸出裡沒有 0c-update 的錯誤行時，補上結束碼與可能原因（#138：
; 程序在建立排程後沒留下任何訊息就結束，推測是被防毒軟體之類的外部程式中止）。
Function DescribeFailure
  Push $2
  Push $3
  ${If} $0 == "error"
    StrCpy $1 "無法執行安裝元件 0c-update，可能被防毒軟體封鎖。$\r$\n$1"
    Goto described
  ${EndIf}
  StrCpy $2 0
  search:
    StrCpy $3 $1 11 $2
    StrCmp $3 "" unexplained
    StrCmp $3 "0c-update: " described
    IntOp $2 $2 + 1
    Goto search
  unexplained:
    StrCpy $3 $0
    IntCmpU $0 255 code code 0
    IntFmt $3 "0x%08X" $0 ; NTSTATUS 之類的大數字以十六進位顯示，方便查詢
  code:
    StrCpy $1 "$1$\r$\n安裝元件意外結束（代碼 $3），沒有留下錯誤訊息；可能是防毒軟體中止了它。"
  described:
  Pop $3
  Pop $2
FunctionEnd

Section "安裝"
  StrCpy $PreviousEntry "unknown"
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "0c-update.exe"
  File "payload.zip"
  ; Write bootstrap files before changing registration, so extraction errors stop early.
  SetOutPath "$INSTDIR"
  StrCpy $1 "無法寫入安裝目錄的檔案。"
  File /oname=maintenance.exe "0c-update.exe"
  File "README.txt"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  IfErrors install_failed
  ; 安裝前就登記「已安裝的應用程式」與開始功能表（#138）：安裝程序中途被中止時，已裝上的部分仍能
  ; 從那裡移除。原本沒有這個項目的話先寫入本版版本；已有的項目保留版本號，由 0c-update 安裝後改成
  ; 實際裝上的版本（syncUninstallEntry），安裝沒成功時才不會顯示沒裝上的版本。
  ReadRegStr $0 HKCU "${ARP_KEY}" "DisplayName"
  ${If} ${Errors}
    StrCpy $PreviousEntry "none"
  ${Else}
    StrCpy $PreviousEntry "present"
  ${EndIf}
  ClearErrors
  StrCpy $1 "無法登記「已安裝的應用程式」或建立開始功能表捷徑。"
  WriteRegStr HKCU "${ARP_KEY}" "DisplayName" "靈犀注音（0c）"
  ${If} $PreviousEntry == "none"
    WriteRegStr HKCU "${ARP_KEY}" "DisplayVersion" "${VERSION}"
  ${EndIf}
  WriteRegStr HKCU "${ARP_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKCU "${ARP_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${ARP_KEY}" "UninstallString" '$\"$INSTDIR\uninstall.exe$\"'
  WriteRegStr HKCU "${ARP_KEY}" "QuietUninstallString" '$\"$INSTDIR\uninstall.exe$\" /S'
  WriteRegDWORD HKCU "${ARP_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${ARP_KEY}" "NoRepair" 1
  CreateDirectory "$SMPROGRAMS\靈犀注音"
  CreateShortcut "$SMPROGRAMS\靈犀注音\使用說明.lnk" "$WINDIR\notepad.exe" '$\"$INSTDIR\README.txt$\"'
  CreateShortcut "$SMPROGRAMS\靈犀注音\解除安裝.lnk" "$INSTDIR\uninstall.exe"
  IfErrors install_failed
  DetailPrint "正在驗證套件、安裝配對字典及註冊輸入法…"
  nsExec::ExecToStack '"$PLUGINSDIR\0c-update.exe" apply -archive "$PLUGINSDIR\payload.zip" -sha256 ${ARCHIVE_SHA256} -channel ${CHANNEL}'
  Pop $0
  Pop $1
  Call TidyOutput
  DetailPrint "$1"
  StrCmp $0 "0" install_success
  Call DescribeFailure
install_failed:
  SetErrorLevel 1
  StrCpy $2 ""
  ${If} $PreviousEntry == "none"
  ${AndIfNot} ${FileExists} "${RECEIPT}"
    ; 什麼都還沒裝上：收回剛登記的項目，回到安裝前的樣子。
    DeleteRegKey HKCU "${ARP_KEY}"
    Delete "$SMPROGRAMS\靈犀注音\使用說明.lnk"
    Delete "$SMPROGRAMS\靈犀注音\解除安裝.lnk"
    RMDir "$SMPROGRAMS\靈犀注音"
  ${ElseIf} $PreviousEntry != "unknown"
    StrCpy $2 "$\r$\n$\r$\n已裝上的部分可在 Windows「已安裝的應用程式」解除安裝靈犀注音。"
  ${EndIf}
  ; 詳細資訊預設隱藏：把 0c-update 的訊息直接放進對話框，使用者截圖就看得到原因（#82）。
  MessageBox MB_ICONSTOP "安裝未完成：$\r$\n$\r$\n$1$2$\r$\n$\r$\n修正後可再次執行此安裝程式；回報問題時請附上這個畫面。" /SD IDOK
  Abort
install_success:
  ClearErrors
  WriteRegStr HKCU "${ARP_KEY}" "DisplayVersion" "${VERSION}"
  SetErrorLevel 0
SectionEnd

Function un.onInit
  SetRegView 64
  SetShellVarContext current
  !insertmacro RequestUnicodeOutput
  ReadRegStr $0 HKCU "${ARP_KEY}" "InstallLocation"
  ${If} $0 != $INSTDIR
    MessageBox MB_ICONSTOP "請由 Windows「已安裝的應用程式」開啟目前版本的解除安裝，並使用原安裝帳號。" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
FunctionEnd

Section "Uninstall"
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "0c-update.exe"
  ; 優先用穩定副本：它隨自動更新換新，解除安裝的修正不必重新執行安裝程式才拿得到（#82）；
  ; 本程式內附的是安裝當時的版本。穩定副本與本程式同在使用者可寫的目錄，沒有新增提權面。
  StrCpy $3 ""
  StrCpy $2 "$LOCALAPPDATA\Zuin\bin\0c-update.exe"
  ${If} ${FileExists} "$2"
    nsExec::ExecToStack '"$2" uninstall'
    Pop $0
    Pop $1
    ${If} $0 == "0"
      Goto uninstalled
    ${EndIf}
    StrCpy $3 $1
  ${EndIf}
  nsExec::ExecToStack '"$PLUGINSDIR\0c-update.exe" uninstall'
  Pop $0
  Pop $1
  ${If} $0 != "0"
  ${AndIf} $3 != ""
    StrCpy $1 $3 ; 兩份都失敗時顯示較新的穩定副本的訊息
  ${EndIf}
uninstalled:
  Call un.TidyOutput
  DetailPrint "$1"
  ${If} $0 != "0"
    SetErrorLevel 1
    MessageBox MB_ICONSTOP "解除安裝未完成：$\r$\n$\r$\n$1$\r$\n$\r$\n個人詞庫會保留；回報問題時請附上這個畫面。" /SD IDOK
    Abort
  ${EndIf}
  DeleteRegKey HKCU "${ARP_KEY}"
  Delete "$SMPROGRAMS\靈犀注音\使用說明.lnk"
  Delete "$SMPROGRAMS\靈犀注音\解除安裝.lnk"
  ; 「靈犀注音設定（0c）」由 0c-update 建立，解除安裝時它依標記檔移除；這裡是保險：安裝程序在建立捷徑
  ; 之後、寫入標記檔之前被中止時（#138）只剩捷徑。舊名稱「靈犀注音設定」一併刪除；不存在不是錯誤。
  Delete "$SMPROGRAMS\靈犀注音\靈犀注音設定（0c）.lnk"
  Delete "$SMPROGRAMS\靈犀注音\靈犀注音設定.lnk"
  RMDir "$SMPROGRAMS\靈犀注音"
  Delete "$INSTDIR\maintenance.exe"
  Delete "$INSTDIR\README.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  SetErrorLevel 0
SectionEnd
