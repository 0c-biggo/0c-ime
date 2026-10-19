# Edge 互動桌面驗收：專用瀏覽器設定目錄與本機頁面；不接觸日常瀏覽資料。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';browser=''}
$browser=$null;$socket=$null
try {
 Add-Type -AssemblyName System.Windows.Forms
 Add-Type @'
using System;
using System.Net.WebSockets;
using System.Text;
using System.Threading;
using System.Runtime.InteropServices;
public class BrowserProbe:IDisposable {
 ClientWebSocket ws = new ClientWebSocket();
 public BrowserProbe(string url) { ws.ConnectAsync(new Uri(url),CancellationToken.None).GetAwaiter().GetResult(); }
 public string Call(string json,int id) {
  using(var timeout=new CancellationTokenSource(10000)) {
   byte[] bytes=Encoding.UTF8.GetBytes(json); ws.SendAsync(new ArraySegment<byte>(bytes),WebSocketMessageType.Text,true,timeout.Token).GetAwaiter().GetResult();
   while(true) {
    var text=new StringBuilder(); WebSocketReceiveResult r; byte[] b=new byte[65536];
    do { r=ws.ReceiveAsync(new ArraySegment<byte>(b),timeout.Token).GetAwaiter().GetResult();text.Append(Encoding.UTF8.GetString(b,0,r.Count)); }while(!r.EndOfMessage);
    string msg=text.ToString();if(msg.StartsWith("{\"id\":"+id+","))return msg;
   }
  }
 }
 public void Dispose(){ws.Dispose();}
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern void keybd_event(byte key,byte scan,uint flags,UIntPtr extra);
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
 public delegate bool EnumProc(IntPtr h, IntPtr l);
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc p, IntPtr l);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr h, out RECT r);
 // 看得到的 0c-ui 候選窗（#144：Chromium 的 GetTextExt 晚到時不能整個不顯示）。
 public static bool CandidateVisible(){
  bool found=false;
  EnumWindows((h,l)=>{ var s=new StringBuilder(64); GetClassName(h,s,64); RECT r; if(IsWindowVisible(h) && s.ToString()=="ZuinCandidateWindow" && GetWindowRect(h, out r) && r.Right>r.Left+20){ found=true; return false; } return true; }, IntPtr.Zero);
  return found;
 }
 public static void Type(string text){foreach(char c in text){byte k=(byte)char.ToUpperInvariant(c); keybd_event(k,0,0,UIntPtr.Zero); keybd_event(k,0,2,UIntPtr.Zero);}}
}
'@
 $title="0c browser acceptance " + (Split-Path $ResultDir -Leaf)
 $html='<html><head><meta charset="utf-8"><title>0c browser acceptance</title></head><body><h1>0c browser acceptance</h1><textarea id="text" autofocus style="font-size:28px;width:500px;height:150px"></textarea><br><input id="password" type="password"></body></html>'
 $html=$html.Replace("<title>0c browser acceptance</title>","<title>$title</title>")
 [IO.File]::WriteAllText("$ResultDir\browser.html",$html)
 $profile="$ResultDir\edge-profile"
 $exe='C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe'
 $report.browser=(Get-Item $exe).VersionInfo.ProductVersion
 $browser=Start-Process $exe -ArgumentList @('--no-first-run','--no-default-browser-check','--disable-background-networking','--remote-debugging-port=0',('--user-data-dir="'+$profile+'"'),('--app="file:///'+("$ResultDir\browser.html" -replace '\\','/')+'"')) -PassThru
 $deadline=(Get-Date).AddSeconds(25)
 while(-not (Test-Path "$profile\DevToolsActivePort") -and (Get-Date) -lt $deadline){Start-Sleep -Milliseconds 200}
 $port=(Get-Content "$profile\DevToolsActivePort")[0]
 $page=$null
 do {
  $pages=Invoke-RestMethod "http://localhost:$port/json/list"
  $page=$pages | Where-Object { $_.url -like '*browser.html' } | Select-Object -First 1
  if(-not $page){Start-Sleep -Milliseconds 200}
 }while(-not $page -and (Get-Date) -lt $deadline)
 if(-not $page){throw 'test browser page missing'}
 $socket=[BrowserProbe]::new([string]$page.webSocketDebuggerUrl)
 $script:counter=0
 function CDP([string]$method,$params=@{}){ $script:counter++;$j=@{id=$script:counter;method=$method;params=$params}|ConvertTo-Json -Depth 6 -Compress;$r=$socket.Call($j,$script:counter)|ConvertFrom-Json;if($r.error){throw ($r.error|ConvertTo-Json)};return $r.result }
 function JS([string]$expression){$r=CDP 'Runtime.evaluate' @{expression=$expression;returnByValue=$true};if($r.exceptionDetails){throw ($r.exceptionDetails|ConvertTo-Json -Depth 3)};return $r.result.value}
 $own=Get-Process msedge | Where-Object MainWindowTitle -like "*$title*" | Select-Object -First 1
 if(-not $own){throw 'test browser window missing'}
 [BrowserProbe]::SetForegroundWindow($own.MainWindowHandle)|Out-Null
 function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
 function SendText([string]$text){if([BrowserProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'browser test window lost focus'};[BrowserProbe]::Type($text);Pump 600}
 function Key([byte]$vk){if([BrowserProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'browser test window lost focus'};[BrowserProbe]::keybd_event($vk,0,0,[UIntPtr]::Zero);[BrowserProbe]::keybd_event($vk,0,2,[UIntPtr]::Zero);Pump 400}
 function Expect([string]$expected,[string]$name){$actual=JS 'document.getElementById("text").value';if($actual -ne $expected){throw "$name expected [$expected], got [$actual]"};$report.checks+=$name}
 CDP 'Page.bringToFront' @{}|Out-Null
 JS 'document.getElementById("text").focus()'|Out-Null;Pump 700
 if (-not (JS 'document.hasFocus()')) { throw 'browser document not focused' }
 SendText 'su3cl3';Expect '你好' 'edge_compose_nihao'
 Key 13;Expect '你好' 'edge_commit'
 JS 'document.getElementById("text").value=""'|Out-Null
 SendText 'su3';Key 27;Expect '' 'edge_cancel'
 SendText 'su3';Key 40
 $w=[Diagnostics.Stopwatch]::StartNew();while(-not [BrowserProbe]::CandidateVisible() -and $w.ElapsedMilliseconds -lt 2000){Pump 50}
 if(-not [BrowserProbe]::CandidateVisible()){throw 'Edge candidate window not visible'}
 $report.checks+='edge_candidate_visible'
 Key 13;Key 13;Expect '你' 'edge_candidate_enter'
 JS 'document.getElementById("password").focus()'|Out-Null;Pump 400
 SendText 'su3cl3'
 if((JS 'document.getElementById("password").value') -ne 'su3cl3'){throw 'browser password intercepted'}
 $report.checks+='edge_password_passthrough'
 JS 'document.getElementById("password").value="";document.getElementById("text").value="";document.getElementById("text").focus()'|Out-Null;Pump 300
 SendText 'su3';Key 13;Expect '你' 'edge_after_password'
 $shot=CDP 'Page.captureScreenshot' @{}
 [IO.File]::WriteAllBytes("$ResultDir\edge.png",[Convert]::FromBase64String($shot.data))
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 if($socket){try{CDP 'Browser.close' @{}|Out-Null}catch{};$socket.Dispose()}
 if($browser -and -not $browser.HasExited){Stop-Process -Id $browser.Id -ErrorAction SilentlyContinue}
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\browser-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
