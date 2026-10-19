# 明確的互動測試機操作：暫時切換 AppsUseLightTheme，finally 原樣還原。
# 只通知本次候選窗重繪，不向其他視窗廣播；截圖由 WM_PRINTCLIENT 取得。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';theme_restored=$false}
$ui=$null;$form=$null;$saved=$false;$ownedUI=$false
$key='HKCU:\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize'
$property='AppsUseLightTheme'
try {
    # 0.1.0-dev.62 起 DLL 註冊在固定路徑 Zuin\tsf（#121），0c-ui 仍在收據記錄的版本目錄。
    $executable=Join-Path (Split-Path $ExpectedDLL) '0c-ui.exe'
    if(-not (Test-Path -LiteralPath $executable)){
        $receipt=Get-Content (Join-Path $env:LOCALAPPDATA 'Zuin\install-receipt.json') -Raw -Encoding UTF8|ConvertFrom-Json
        $executable=[IO.Path]::GetFullPath((Join-Path (Join-Path (Join-Path $env:LOCALAPPDATA 'Zuin') $receipt.directory) '0c-ui.exe'))
    }
    $existing=@(Get-Process 0c-ui -ErrorAction SilentlyContinue)
    if($existing|Where-Object Path -ne $executable){throw 'Unrelated candidate UI is running'}
    $ui=$existing|Select-Object -First 1
    Add-Type -AssemblyName System.Windows.Forms,System.Drawing
    Add-Type -ReferencedAssemblies System.Drawing,System.Windows.Forms -TypeDefinition @'
using System; using System.Runtime.InteropServices; using System.Drawing;
public class CandidateHostForm : System.Windows.Forms.Form {
 public long Selected=-1;
 protected override void WndProc(ref System.Windows.Forms.Message m) {
   if(m.Msg==0x8037) Selected=m.LParam.ToInt64();
   base.WndProc(ref m);
 }
}
public static class CandidateProbe {
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
 [StructLayout(LayoutKind.Sequential)] public struct Contrast { public uint Size,Flags; public IntPtr Scheme; }
 [DllImport("user32.dll")] public static extern bool SetProcessDpiAwarenessContext(IntPtr context);
 [DllImport("user32.dll")] public static extern bool SystemParametersInfo(uint action,uint param,ref Contrast value,uint flags);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string cls,string title);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hwnd,IntPtr dc,uint flags);
 [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr hwnd,out Rect rect);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hwnd,out Rect rect);
 [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 public static Bitmap Capture(IntPtr hwnd) {
   Rect r; if(!GetClientRect(hwnd,out r))throw new Exception("client bounds unavailable");
   var b=new Bitmap(r.Right,r.Bottom);
   using(var g=Graphics.FromImage(b)) {var dc=g.GetHdc();try{if(!PrintWindow(hwnd,dc,3))throw new Exception("candidate capture failed");}finally{g.ReleaseHdc(dc);}}
   return b;
 }
}
'@
    [CandidateProbe]::SetProcessDpiAwarenessContext([IntPtr](-4))|Out-Null
    $hc=[CandidateProbe+Contrast]::new();$hc.Size=[Runtime.InteropServices.Marshal]::SizeOf($hc)
    if(-not [CandidateProbe]::SystemParametersInfo(0x42,$hc.Size,[ref]$hc,0)){throw 'Cannot query contrast theme'}
    if($hc.Flags -band 1){throw 'Light/dark acceptance requires contrast mode off; contrast palette is separately unit tested'}
    $keyExisted=Test-Path $key
    $settings=Get-ItemProperty $key -ErrorAction SilentlyContinue
    $original=if($settings){$settings.PSObject.Properties[$property]}else{$null}
    $hadValue=$null -ne $original
    if($hadValue){$originalValue=$original.Value;$originalKind=(Get-Item $key).GetValueKind($property)}
    $saved=$true
    if(-not $keyExisted){New-Item $key -Force|Out-Null}
    $form=[CandidateHostForm]::new();$form.Text='0c candidate appearance acceptance';$form.Width=500;$form.Height=180
    $label=[Windows.Forms.Label]::new();$label.Text='Candidate appearance acceptance';$label.AutoSize=$true;$form.Controls.Add($label)
    $form.Show();$form.Activate()
    function Pump([int]$ms){$clock=[Diagnostics.Stopwatch]::StartNew();while($clock.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
    function FocusGuard {if([CandidateProbe]::GetForegroundWindow() -ne $form.Handle){throw 'Candidate test host lost focus'}}
    if(-not $ui){$ui=Start-Process $executable -PassThru;$ownedUI=$true}
    $deadline=(Get-Date).AddSeconds(10)
    do {Pump 100;$candidate=[CandidateProbe]::FindWindow('ZuinCandidateWindow','Zuin')}while($candidate -eq [IntPtr]::Zero -and (Get-Date) -lt $deadline)
    if($candidate -eq [IntPtr]::Zero){throw 'Candidate window not created'}
    $report.ui=$ui.Path
    if($ui.Path -ne $executable){throw 'Unexpected candidate UI executable'}
    $report.dpi=[CandidateProbe]::GetDpiForWindow($candidate)
    $area=[Windows.Forms.Screen]::FromHandle($form.Handle).WorkingArea
    $message=@{show=$true;x=$area.Right-4;y=$area.Bottom-4;w=0;h=20;candidates=@(@{id=1;text='你好'},@{id=2;text='妳好'});highlight=0;page=1;pages=2;suggestion='麻婆豆腐';owner=$PID;target=0;revision=1}
    function Present($value){
        FocusGuard
        $pipe=[IO.Pipes.NamedPipeClientStream]::new('.','0c-ui-'+[Security.Principal.WindowsIdentity]::GetCurrent().User.Value,[IO.Pipes.PipeDirection]::Out)
        try {
            $pipe.Connect(3000)
            $bytes=[Text.Encoding]::UTF8.GetBytes(($value|ConvertTo-Json -Depth 5 -Compress));$header=[BitConverter]::GetBytes([int]$bytes.Length)
            $pipe.Write($header,0,4);$pipe.Write($bytes,0,$bytes.Length);$pipe.Flush()
        } finally {$pipe.Dispose()}
        Pump 300
    }
    foreach($mode in @(@{name='light';value=1;background=0xFFFFFF},@{name='dark';value=0;background=0x202020},@{name='light-again';value=1;background=0xFFFFFF})){
        Set-ItemProperty $key $property -Type DWord -Value $mode.value
        [CandidateProbe]::SendMessage($candidate,0x1A,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null
        Present $message
        if(-not[CandidateProbe]::IsWindowVisible($candidate)){throw 'Candidate window was not shown'}
        $bitmap=[CandidateProbe]::Capture($candidate)
        try {
            $color=$bitmap.GetPixel(2,2).ToArgb() -band 0xFFFFFF
            if($color -ne $mode.background){throw "$($mode.name) candidate background differs ($color)"}
            $bitmap.Save((Join-Path $ResultDir ($mode.name+'.png')),[Drawing.Imaging.ImageFormat]::Png)
        } finally {$bitmap.Dispose()}
        $report.checks+='candidate_'+$mode.name
    }
    # 從候選窗本身擷取；黃色像素不能由選取背景或單色文字產生。
    $emoji=$message.Clone();$emoji.columns=5;$emoji.kind='emoji';$emoji.title='Tab → 顏文字';$emoji.suggestion=''
    $emoji.candidates=@(@{id=0;text='='},@{id=1;text=[char]::ConvertFromUtf32(0x1F600)},@{id=2;text='👩🏽‍💻'},@{id=3;text='🇹🇼'},@{id=4;text='👨‍👩‍👧‍👦'},@{id=5;text='😀'},@{id=6;text='😁'},@{id=7;text='😂'},@{id=8;text='😃'},@{id=9;text='😄'})
    $emojiModes=@(@{name='emoji-light';value=1},@{name='emoji-dark';value=0})
    foreach($mode in $emojiModes){
        Set-ItemProperty $key $property -Type DWord -Value $mode.value
        [CandidateProbe]::SendMessage($candidate,0x1A,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null
        Present $emoji
        $bitmap=[CandidateProbe]::Capture($candidate)
        try {
            $yellow=0
            for($x=0;$x -lt $bitmap.Width;$x++){for($y=0;$y -lt $bitmap.Height;$y++){
                $pixel=$bitmap.GetPixel($x,$y)
                if($pixel.R -gt 180 -and $pixel.G -gt 100 -and $pixel.B -lt 100){$yellow++}
            }}
            if($yellow -lt 50){throw "$($mode.name) has no color Emoji ($yellow yellow pixels)"}
            $bitmap.Save((Join-Path $ResultDir ($mode.name+'.png')),[Drawing.Imaging.ImageFormat]::Png)
            $report.checks+=$mode.name+'_color_glyphs'
        } finally {$bitmap.Dispose()}
    }
    # 確認回報的 😆 自己有彩色像素，不靠其他候選的黃色判斷通過。
    $face=$emoji.Clone();$face.candidates=@(@{id=1;text=[char]::ConvertFromUtf32(0x1F606)})
    foreach($columns in @(0,1,5)){
        $face.columns=$columns
        foreach($mode in $emojiModes){
            $name='laughing-'+$columns+'-'+$mode.name
            Set-ItemProperty $key $property -Type DWord -Value $mode.value
            [CandidateProbe]::SendMessage($candidate,0x1A,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null
            Present $face
            $bitmap=[CandidateProbe]::Capture($candidate)
            try {
                $yellow=0
                for($x=0;$x -lt $bitmap.Width;$x++){for($y=0;$y -lt $bitmap.Height;$y++){
                    $pixel=$bitmap.GetPixel($x,$y)
                    if($pixel.R -gt 180 -and $pixel.G -gt 100 -and $pixel.B -lt 100){$yellow++}
                }}
                if($yellow -lt 50){throw "$name has no color laughing face ($yellow yellow pixels)"}
                $bitmap.Save((Join-Path $ResultDir ($name+'.png')),[Drawing.Imaging.ImageFormat]::Png)
                $report.checks+=$name+'_color_glyphs'
            } finally {$bitmap.Dispose()}
        }
    }
    # 從實際彩色字形位置點第二列，確認單欄彩色排版與滑鼠命中一致。
    foreach($columns in @(0,1)){
        $pointer=$face.Clone();$pointer.columns=$columns;$pointer.target=$form.Handle.ToInt64()
        $pointer.candidates=@(@{id=101;text=[char]::ConvertFromUtf32(0x1F606)},@{id=102;text=[char]::ConvertFromUtf32(0x1F606)})
        $form.Selected=-1
        Present $pointer
        $bitmap=[CandidateProbe]::Capture($candidate)
        try {
            $bands=@();$start=-1;$last=-1;$minX=$bitmap.Width;$maxX=0
            for($y=0;$y -lt $bitmap.Height;$y++){
                $yellow=0
                for($x=0;$x -lt $bitmap.Width;$x++){
                    $pixel=$bitmap.GetPixel($x,$y)
                    if($pixel.R -gt 180 -and $pixel.G -gt 100 -and $pixel.B -lt 100){$yellow++;$minX=[Math]::Min($minX,$x);$maxX=[Math]::Max($maxX,$x)}
                }
                if($yellow -ge 3){if($start-lt0){$start=$y};$last=$y}
                elseif($start-ge0){$bands+=,@($start,$last);$start=-1}
            }
            if($start-ge0){$bands+=,@($start,$last)}
            if($bands.Count-ne2){throw "single-column $columns should show two separate color faces"}
            $clickX=[int](($minX+$maxX)/2);$clickY=[int](($bands[1][0]+$bands[1][1])/2)
            [CandidateProbe]::SendMessage($candidate,0x202,[IntPtr]::Zero,[IntPtr](($clickY-shl16)-bor$clickX))|Out-Null
            Pump 150
            if($form.Selected-ne102){throw "single-column $columns selected $($form.Selected) from the second color face"}
            $report.checks+='emoji-singlecolumn-'+$columns+'_pointer_matches_color_glyphs'
        } finally {$bitmap.Dispose()}
    }
    Present $message
    $bounds=[CandidateProbe+Rect]::new();[CandidateProbe]::GetWindowRect($candidate,[ref]$bounds)|Out-Null
    if($bounds.Left -lt $area.Left -or $bounds.Top -lt $area.Top -or $bounds.Right -gt $area.Right -or $bounds.Bottom -gt $area.Bottom){throw 'Candidate window extends past work area'}
    $report.checks+='candidate_work_area_bounds'
    $hidden=$message.Clone();$hidden.show=$false;$hidden.owner=$PID+1
    Present $hidden
    if(-not[CandidateProbe]::IsWindowVisible($candidate)){throw 'Unrelated host hid the current candidate window'}
    $report.checks+='candidate_stale_hide_ignored'
    $hidden.owner=$PID
    Present $hidden
    if([CandidateProbe]::IsWindowVisible($candidate)){throw 'Current host could not hide candidate window'}
    $report.checks+='candidate_owner_hide'
    $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
    if($saved){
        if($hadValue){Set-ItemProperty $key $property -Type $originalKind -Value $originalValue}
        else {Remove-ItemProperty $key $property -ErrorAction SilentlyContinue}
        $restored=(Get-ItemProperty $key -ErrorAction SilentlyContinue).PSObject.Properties[$property]
        $report.theme_restored=if($hadValue){$restored.Value -eq $originalValue -and (Get-Item $key).GetValueKind($property) -eq $originalKind}else{$null -eq $restored}
        if(-not $keyExisted -and (Get-Item $key).ValueCount -eq 0 -and (Get-Item $key).SubKeyCount -eq 0){Remove-Item $key}
        if(-not $report.theme_restored){$report.passed=$false;$report.error+='; theme restore failed'}
    }
    if($ownedUI -and $ui -and -not $ui.HasExited){Stop-Process -Id $ui.Id}
    if($form){$form.Close();$form.Dispose()}
    $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\candidate-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
