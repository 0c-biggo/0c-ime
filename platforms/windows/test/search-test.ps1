# Windows 系統搜尋框（SearchHost，AppContainer 沙箱＋沉浸式層級）：DLL 載入、組字、宿主行程內候選窗與鍵盤選字。
# 只在測試機的互動工作階段執行（acceptance-test.ps1 -Suite search）；會重新啟動 SearchHost，結束時按 Esc 關閉搜尋。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';dll='';values=[ordered]@{}}
# SearchHost 在沙箱裡的 LOCALAPPDATA；在此建 Zuin\ 目錄，DLL 才會寫 tsf.log（測完移除）。
$sandbox="$env:LOCALAPPDATA\Packages\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\AC"
$log="$sandbox\Zuin"
$createdLog=$false
try {
 if(-not(Test-Path $sandbox)){throw 'SearchHost sandbox folder missing (Windows 11 search required)'}
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing,UIAutomationClient,UIAutomationTypes
 Add-Type @'
using System; using System.Runtime.InteropServices;
public static class SearchProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
 [DllImport("user32.dll")] static extern void keybd_event(byte k, byte s, uint f, UIntPtr e);
 public static uint ForegroundPid(){ uint p; GetWindowThreadProcessId(GetForegroundWindow(), out p); return p; }
 public static void Key(byte vk){ keybd_event(vk,0,0,UIntPtr.Zero); System.Threading.Thread.Sleep(40); keybd_event(vk,0,2,UIntPtr.Zero); System.Threading.Thread.Sleep(180); }
 public static void Keys(string s){ foreach(char c in s){ Key((byte)char.ToUpperInvariant(c)); } }
 public static void OpenSearch(){ keybd_event(0x5B,0,0,UIntPtr.Zero); keybd_event(0x53,0,0,UIntPtr.Zero); System.Threading.Thread.Sleep(40); keybd_event(0x53,0,2,UIntPtr.Zero); keybd_event(0x5B,0,2,UIntPtr.Zero); }
 // 對整個工作階段切到靈犀注音（搜尋框是另一個行程，只對本行程切換不夠）。
 public static int Activate(){
  object o = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try { Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
   return ((IProfiles)o).ActivateProfile(1,0x0404,ref c,ref p,IntPtr.Zero,0x20000000); } finally { Marshal.FinalReleaseComObject(o); }
 }
}
'@
 function Shot([string]$name){
  $b=[Windows.Forms.Screen]::PrimaryScreen.Bounds
  $bmp=New-Object Drawing.Bitmap $b.Width,$b.Height
  $g=[Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($b.Location,[Drawing.Point]::Empty,$b.Size)
  $bmp.Save((Join-Path $ResultDir "$name.png")); $g.Dispose(); $bmp.Dispose()
 }
 # 搜尋框的文字由 UI Automation 讀取（只讀本次輸入的測試字）。
 function Value {
  $e=[Windows.Automation.AutomationElement]::FocusedElement
  $p=$null
  if($e -and $e.TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern,[ref]$p)){return $p.Current.Value}
  return $null
 }
 function Check([bool]$ok,[string]$name,[string]$detail){ if(-not $ok){throw "$name failed: $detail"}; $script:report.checks+=$name }
 # 以同一個隔離引擎、相同的全新詞庫取得這版資料包的候選；用 Select(ID) 當參考，
 # SearchHost 則真的按數字鍵。參考 session 不提交、不學習，也不依賴第三項一定不是原字。
 function Reference([int]$index){
  $pipeName='0c-'+[Security.Principal.WindowsIdentity]::GetCurrent().User.Value.ToLowerInvariant()
  $pipe=[IO.Pipes.NamedPipeClientStream]::new('.',$pipeName,[IO.Pipes.PipeDirection]::InOut)
  function ReadExact([int]$count){
   $bytes=New-Object byte[] $count;$offset=0
   while($offset -lt $count){$read=$pipe.Read($bytes,$offset,$count-$offset);if($read -eq 0){throw 'Reference engine disconnected'};$offset+=$read}
   return ,$bytes
  }
  function Exchange($request){
   $bytes=[Text.Encoding]::UTF8.GetBytes(($request|ConvertTo-Json -Depth 6 -Compress))
   $header=[BitConverter]::GetBytes([uint32]$bytes.Length)
   $pipe.Write($header,0,4);$pipe.Write($bytes,0,$bytes.Length);$pipe.Flush()
   $length=[BitConverter]::ToUInt32((ReadExact 4),0)
   if($length -gt 1048576){throw 'Reference reply exceeds frame limit'}
   $reply=[Text.Encoding]::UTF8.GetString((ReadExact $length))|ConvertFrom-Json
   if(-not $reply.ok){throw "Reference engine: $($reply.error)"}
   return $reply
  }
  try {
   $pipe.Connect(3000)
   $session=(Exchange @{op='open'}).session
   foreach($key in @('s','u','3','c','l','3','Down')){
    $reply=Exchange @{op='handle';session=$session;req=@{type='key';key=$key;private=$true}}
   }
   $items=@($reply.snapshot.candidates.items)
   if($items.Count -le $index){throw "Reference has only $($items.Count) candidates"}
   $before=-join @($reply.snapshot.preedit|ForEach-Object text)
   $chosen=Exchange @{op='handle';session=$session;req=@{type='select';index=$items[$index].id;private=$true}}
   return [pscustomobject]@{index=$index;candidate=$items[$index];items=$items;before=$before;selected=(-join @($chosen.snapshot.preedit|ForEach-Object text))}
  } finally {$pipe.Dispose()}
 }
 $third=Reference 2
 $alternative=$null
 for($i=0;$i -lt $third.items.Count -and $i -lt 9;$i++){
  $choice=Reference $i
  if($choice.selected -ne $choice.before){$alternative=$choice;break}
 }
 Check ($null -ne $alternative) 'search_has_distinct_candidate_control' 'No candidate changes the composed text'
 $report.reference=[ordered]@{items=$third.items;third=$third.candidate;third_expected=$third.selected;alternative=$alternative.candidate;alternative_key=($alternative.index+1);alternative_expected=$alternative.selected}

 if(-not(Test-Path $log)){New-Item -ItemType Directory $log|Out-Null;$createdLog=$true}
 $logStart=if(Test-Path "$log\tsf.log"){(Get-Item "$log\tsf.log").Length}else{0}
 # 重新啟動 SearchHost，讓它載入目前註冊的 DLL。
 Get-Process SearchHost -ErrorAction SilentlyContinue | Stop-Process -Force
 Start-Sleep -Seconds 3
 $hr=[SearchProbe]::Activate()
 Check ($hr -eq 0) 'search_profile_activated' ('0x{0:X8}' -f $hr)
 Start-Sleep -Milliseconds 800
 [SearchProbe]::OpenSearch(); Start-Sleep -Seconds 3
 $host_=Get-Process -Id ([SearchProbe]::ForegroundPid()) -ErrorAction SilentlyContinue
 Check ($host_ -and $host_.ProcessName -eq 'SearchHost') 'search_box_focused' "$($host_.ProcessName)"
 [SearchProbe]::Keys('su3cl3'); Start-Sleep -Milliseconds 1200
 $report.values.composed=Value
 Check ($report.values.composed -eq '你好') 'search_composes_chinese' "$($report.values.composed)"
 $report.dll=(Get-Process SearchHost | ForEach-Object { $_.Modules } | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 Check ($report.dll -eq $ExpectedDLL) 'search_new_dll_loaded' "$($report.dll)"
 [SearchProbe]::Key(0x28); Start-Sleep -Milliseconds 1200
 Shot 'search-candidates'
 $text=Get-Content "$log\tsf.log" -Raw -Encoding UTF8
 $text=if($text.Length -gt $logStart){$text.Substring([int][Math]::Min($logStart,$text.Length))}else{$text}
 $band=[regex]::Match($text,'宿主行程內候選窗（層級 (\d+)）')
 Check ($band.Success -and [int]$band.Groups[1].Value -gt 1) 'search_candidates_in_host_layer' "$($band.Value)"
 Check ($text -match 'open: session=') 'search_engine_session' 'no session in sandbox log'
 [SearchProbe]::Key(0x33); Start-Sleep -Milliseconds 1200
 $report.values.selected=Value
 Check ($report.values.selected -ceq $third.selected) 'search_keyboard_selects_third_candidate' "expected [$($third.selected)], got [$($report.values.selected)]"
 Shot 'search-selected'
 # 第三項若恰好就是原字，只比對文字還不足以證明按鍵有效。一次 Esc 應清掉整段；
 # 若 3 被漏掉、仍在選字，一次 Esc 只會收起候選並留下「你好」。
 [SearchProbe]::Key(0x1B);Start-Sleep -Milliseconds 500
 Check ((Value) -ceq '') 'search_selection_closed_before_escape' "$(Value)"
 [SearchProbe]::Keys('su3cl3');[SearchProbe]::Key(0x28);Start-Sleep -Milliseconds 800
 Shot 'search-control-candidates'
 [SearchProbe]::Key([byte](0x31+$alternative.index));Start-Sleep -Milliseconds 1200
 $report.values.control=Value
 Check ($report.values.control -ceq $alternative.selected -and $report.values.control -cne $third.before) 'search_keyboard_selects_distinct_candidate' "expected [$($alternative.selected)], got [$($report.values.control)]"
 Shot 'search-control-selected'
 [SearchProbe]::Key(0x1B);Start-Sleep -Milliseconds 500
 Check ((Value) -ceq '') 'search_control_selection_closed_before_escape' "$(Value)"
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 try { [SearchProbe]::Key(0x1B); [SearchProbe]::Key(0x1B); Start-Sleep -Milliseconds 500; [SearchProbe]::Key(0x1B) } catch {}
 if($createdLog){
  # 先關掉 SearchHost，tsf.log 才刪得掉；它會在下次開啟搜尋時自動啟動。
  Get-Process SearchHost -ErrorAction SilentlyContinue | Stop-Process -Force
  Start-Sleep -Seconds 2
  if(Test-Path "$log\tsf.log"){Copy-Item "$log\tsf.log" "$ResultDir\search-tsf.log"}
  Remove-Item $log -Recurse -Force -ErrorAction SilentlyContinue
 }
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\search-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
