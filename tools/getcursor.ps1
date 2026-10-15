Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class CursorUtil {
    [StructLayout(LayoutKind.Sequential)]
    public struct POINT { public int X, Y; }
    [DllImport("user32.dll")]
    public static extern bool GetCursorPos(out POINT p);
}
"@
$p = New-Object CursorUtil+POINT
[CursorUtil]::GetCursorPos([ref]$p) | Out-Null
Write-Output ("{0},{1}" -f $p.X, $p.Y)
