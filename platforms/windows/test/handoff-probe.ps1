# Native keyboard probe for one dedicated, synthetic Windows account. The hook
# records only events in this fixture's foreground window; never daily input.
param([Parameter(Mandatory=$true)][string]$Directory,
      [Parameter(Mandatory=$true)][string]$ExpectedUser, [int]$Seconds = 3600)
$ErrorActionPreference = 'Stop'
if ([Security.Principal.WindowsIdentity]::GetCurrent().Name -ne $ExpectedUser) {
    throw 'wrong test account; refusing desktop capture'
}
$Directory = [IO.Path]::GetFullPath($Directory)
if (-not $Directory.StartsWith($env:USERPROFILE + '\', [StringComparison]::OrdinalIgnoreCase)) {
    throw 'evidence directory must be inside the dedicated test profile'
}
Add-Type -AssemblyName PresentationFramework, PresentationCore, WindowsBase
Add-Type @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices;
using System.Threading;
public static class HandoffProbe {
  [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IProfiles {
    [PreserveSig] int ActivateProfile(uint t, ushort l, ref Guid c, ref Guid p, IntPtr h, uint f);
    [PreserveSig] int DeactivateProfile(uint t, ushort l, ref Guid c, ref Guid p, IntPtr h, uint f);
    [PreserveSig] int GetProfile(uint t, ushort l, ref Guid c, ref Guid p, IntPtr h, out Profile r);
    [PreserveSig] int EnumProfiles(ushort l, out IntPtr e);
    [PreserveSig] int ReleaseInputProcessor(ref Guid c, uint f);
    [PreserveSig] int RegisterProfile(IntPtr unused);
    [PreserveSig] int UnregisterProfile(ref Guid c, ushort l, ref Guid p, uint f);
    [PreserveSig] int GetActiveProfile(ref Guid category, out Profile p);
  }
  [StructLayout(LayoutKind.Sequential)] public struct Profile {
    public uint type; public ushort language; public Guid clsid, profile, category;
    public IntPtr substitute; public uint caps; public IntPtr layout; public uint flags;
  }
  [StructLayout(LayoutKind.Sequential)] struct KeyData { public uint vk, scan, flags, time; public UIntPtr extra; }
  public class Event { public string utc; public uint vk, scan, flags, time; public ulong extra; public string kind; }
  static readonly List<Event> events = new List<Event>();
  static readonly object sync = new object();
  delegate IntPtr Hook(int code, IntPtr w, IntPtr l);
  static Hook callback = Observe;
  static IntPtr hook, window;
  static Thread hookThread;
  static uint hookThreadId;
  [StructLayout(LayoutKind.Sequential)] struct Message {
    public IntPtr hwnd; public uint message; public UIntPtr w; public IntPtr l;
    public uint time; public int x, y; public uint privateData;
  }
  public static volatile bool Done;
  public static string Error;
  public static string ReleaseUtc;
  const ulong Cookie = 0x00303348;
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] static extern IntPtr SetWindowsHookEx(int id, Hook cb, IntPtr module, uint thread);
  [DllImport("user32.dll")] static extern bool UnhookWindowsHookEx(IntPtr h);
  [DllImport("user32.dll")] static extern IntPtr CallNextHookEx(IntPtr h, int code, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] static extern uint MapVirtualKey(uint key, uint kind);
  [DllImport("user32.dll")] static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode)] static extern IntPtr GetModuleHandle(string name);
  [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
  [DllImport("user32.dll")] static extern int GetMessage(out Message m, IntPtr hwnd, uint min, uint max);
  [DllImport("user32.dll")] static extern bool TranslateMessage(ref Message m);
  [DllImport("user32.dll")] static extern IntPtr DispatchMessage(ref Message m);
  [DllImport("user32.dll")] static extern bool PostThreadMessage(uint thread, uint message, UIntPtr w, IntPtr l);
  static IntPtr Observe(int code, IntPtr w, IntPtr l) {
    if(code >= 0 && GetForegroundWindow() == window) {
      var k = (KeyData)Marshal.PtrToStructure(l, typeof(KeyData));
      lock(sync) events.Add(new Event { utc=DateTime.UtcNow.ToString("o"), vk=k.vk, scan=k.scan,
        flags=k.flags, time=k.time, extra=k.extra.ToUInt64(), kind=w.ToInt64().ToString() });
    }
    return CallNextHookEx(hook, code, w, l);
  }
  public static void Start(IntPtr hwnd) {
    window=hwnd;
    var ready=new ManualResetEvent(false);
    // Keep the low-level hook responsive while TSF waits for an IPC reply.
    // Installing it on the UI thread would distort cold-start/handoff input.
    hookThread=new Thread(delegate() {
      hookThreadId=GetCurrentThreadId();
      hook=SetWindowsHookEx(13, callback, GetModuleHandle(null), 0);
      ready.Set(); Message m;
      while(GetMessage(out m,IntPtr.Zero,0,0)>0) { TranslateMessage(ref m); DispatchMessage(ref m); }
      if(hook!=IntPtr.Zero) UnhookWindowsHookEx(hook);
    }) { IsBackground=true };
    hookThread.Start();
    if(!ready.WaitOne(1000)) throw new Exception("keyboard hook did not start");
    if(hook==IntPtr.Zero) throw new Exception("keyboard hook failed");
  }
  public static void Stop() {
    if(hookThread!=null) { PostThreadMessage(hookThreadId,0x12,UIntPtr.Zero,IntPtr.Zero); hookThread.Join(1000); }
  }
  public static Event[] Events() { lock(sync) return events.ToArray(); }
  public static Profile Active(bool activate) {
    object obj=Activator.CreateInstance(System.Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
    try {
      var manager=(IProfiles)obj;
      Guid clsid=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), profile=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
      if(activate) Marshal.ThrowExceptionForHR(manager.ActivateProfile(1,0x0404,ref clsid,ref profile,IntPtr.Zero,0x10000000));
      Guid category=new Guid("34745C63-B2F0-4784-8B67-5E12C8701A31"); Profile p;
      Marshal.ThrowExceptionForHR(manager.GetActiveProfile(ref category,out p)); return p;
    } finally { Marshal.FinalReleaseComObject(obj); }
  }
  public static void Type(string keys, int interval, string release, string run, int delay) {
    Done=false; Error=""; ReleaseUtc=""; lock(sync) events.Clear();
    // The worker can release the gate while the UI thread waits for its first
    // TSF RPC. The native hook proves when the first key reached the host.
    new Thread(delegate() {
      try {
        foreach(char ch in keys) {
          if(GetForegroundWindow()!=window) throw new Exception("fixture lost foreground; refusing further keys");
          byte vk=ch=='\n'?(byte)13:(byte)Char.ToUpperInvariant(ch);
          keybd_event(vk,(byte)MapVirtualKey(vk,0),0,new UIntPtr(Cookie));
          keybd_event(vk,(byte)MapVirtualKey(vk,0),2,new UIntPtr(Cookie));
          if(interval>0) Thread.Sleep(interval);
        }
        if(!String.IsNullOrEmpty(release)) {
          Thread.Sleep(delay); File.WriteAllText(release,run);
          ReleaseUtc=DateTime.UtcNow.ToString("o");
        }
      } catch(Exception e) { Error=e.Message; }
      finally { Done=true; }
    }) { IsBackground=true }.Start();
  }
}
'@
New-Item -ItemType Directory $Directory -Force | Out-Null
$win = New-Object Windows.Window
$win.Title = "0c synthetic handoff probe ($PID)"; $win.Width = 650; $win.Height = 180; $win.Topmost = $true
$box = New-Object Windows.Controls.TextBox; $box.FontSize = 28; $win.Content = $box
$win.Show(); $win.Activate() | Out-Null; $box.Focus() | Out-Null
$handle = (New-Object Windows.Interop.WindowInteropHelper($win)).Handle
$win.add_Closed({ [Windows.Threading.Dispatcher]::CurrentDispatcher.BeginInvokeShutdown([Windows.Threading.DispatcherPriority]::Background) })
$timer = New-Object Windows.Threading.DispatcherTimer
$timer.Interval = [TimeSpan]::FromMilliseconds(10)
$state = @{ command = $null; finish = $null; history = @(); started = [DateTime]::UtcNow; seq = 0 }
$box.add_TextChanged({ $state.history += [ordered]@{ utc=[DateTime]::UtcNow.ToString('o'); text=$box.Text } }.GetNewClosure())
$timer.add_Tick({
    try {
        if ($state.command) {
            if ([HandoffProbe]::Done -and -not $state.finish) { $state.finish = [DateTime]::UtcNow.AddMilliseconds(800) }
            if ($state.finish -and [DateTime]::UtcNow -ge $state.finish) {
                $c = $state.command
                $observed = @([HandoffProbe]::Events() | Where-Object { $_.kind -eq '256' } | ForEach-Object { $_.vk })
                $expected = @($c.keys.ToCharArray() | ForEach-Object { if ($_ -eq "`n") { 13 } else { [int][char]::ToUpperInvariant($_) } })
                $inputComplete = (($observed -join ',') -eq ($expected -join ','))
                $foreground = ([HandoffProbe]::GetForegroundWindow() -eq $handle)
                $r = [ordered]@{ name=$c.name; keys=$c.keys; interval_ms=$c.interval_ms; text=$box.Text; expected=$c.expected
                    passed=($box.Text -eq $c.expected -and $inputComplete -and $foreground -and -not [HandoffProbe]::Error); error=[HandoffProbe]::Error
                    input_complete=$inputComplete; input_mode=$c.input_mode
                    pid=$PID; session=[Diagnostics.Process]::GetCurrentProcess().SessionId; utc=[DateTime]::UtcNow.ToString('o')
                    profile=$state.profile; foreground=$foreground; activated_profile=($c.activate_profile -eq $true)
                    release_utc=[HandoffProbe]::ReleaseUtc; events=[HandoffProbe]::Events(); text_changes=$state.history }
                $r | ConvertTo-Json -Depth 8 | Set-Content "$Directory\$($c.name).json" -Encoding UTF8
                $state.command=$null; $state.finish=$null
            }
        } elseif (Test-Path "$Directory\command.json") {
            $c=Get-Content "$Directory\command.json" -Raw -Encoding UTF8 | ConvertFrom-Json
            if ($c.gate_ready -and -not (Test-Path $c.gate_ready)) { return }
            if ($c.name -notmatch '^[a-zA-Z0-9_-]{1,64}$' -or $c.keys -notmatch '^[a-z0-9\n]+$' -or $c.interval_ms -lt 0 -or $c.interval_ms -gt 500) { throw 'invalid probe command' }
            if (Test-Path "$Directory\$($c.name).json") { throw 'refusing to overwrite a result' }
            Remove-Item "$Directory\command.json"
            if ([HandoffProbe]::GetForegroundWindow() -ne $handle) { throw 'fixture is not foreground; refusing keys' }
            $p=[HandoffProbe]::Active($c.activate_profile -eq $true)
            if ($p.profile -ne [guid]'EF62F743-9855-4C94-A658-1F0F98B7B840') { throw 'active profile is not 0c' }
            if ([HandoffProbe]::GetForegroundWindow() -ne $handle) { throw 'fixture is not foreground; refusing keys' }
            if ($c.clear -ne $false) { $box.Clear() }
            $state.history=@(); $state.profile=$p; $state.command=$c
            if ($c.input_mode -eq 'rdp') {
                $state.finish=[DateTime]::UtcNow.AddSeconds(5)
                [HandoffProbe]::Type('',0,'','',0)
                Set-Content "$Directory\$($c.name).waiting" 'waiting'
            } else {
                [HandoffProbe]::Type($c.keys,$c.interval_ms,$c.gate_release,$c.run,$c.release_delay_ms)
            }
        }
        if ([DateTime]::UtcNow -ge $state.started.AddSeconds($Seconds)) { $win.Close() }
    } catch {
        $_ | Out-String | Add-Content "$Directory\error.log"
        $timer.Stop(); $win.Close()
    }
}.GetNewClosure())
try {
    [HandoffProbe]::Active($true) | Out-Null
    [HandoffProbe]::Start($handle)
    [ordered]@{pid=$PID;session=[Diagnostics.Process]::GetCurrentProcess().SessionId;profile=[HandoffProbe]::Active($false)} | ConvertTo-Json -Depth 4 | Set-Content "$Directory\ready.json" -Encoding UTF8
    $timer.Start()
    [Windows.Threading.Dispatcher]::Run()
} finally { $timer.Stop(); [HandoffProbe]::Stop(); $win.Close() }
