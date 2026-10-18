# Runs a command and reports its peak memory, for scripts/bench/run.ts on Windows: the largest
# peak working set of the process or any of its descendants (npm bin shims start the real
# compiler as a child). Output goes to -Out; stdout gets {"seconds":..,"peakBytes":..,"exitCode":..}.
# Arguments come as a JSON array, since PowerShell would bind ones like -p to its own parameters.
param(
	[Parameter(Mandatory)] [string]$Out,
	[Parameter(Mandatory)] [string]$Command,
	[string]$ArgumentsJson = '[]'
)

# Descendants join the process's job object. A handle opened while a process runs still reads
# its peak working set after it exits, so short-lived children are measured too.
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;

public static class PeakMemory {
	[DllImport("kernel32.dll", SetLastError = true)]
	static extern IntPtr CreateJobObject(IntPtr attributes, string name);
	[DllImport("kernel32.dll", SetLastError = true)]
	static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
	[DllImport("kernel32.dll", SetLastError = true)]
	static extern bool QueryInformationJobObject(IntPtr job, int infoClass, IntPtr info, int length, IntPtr returned);
	[DllImport("kernel32.dll", SetLastError = true)]
	static extern IntPtr OpenProcess(int access, bool inherit, int pid);
	[DllImport("psapi.dll", SetLastError = true)]
	static extern bool GetProcessMemoryInfo(IntPtr process, out Counters counters, int size);

	[StructLayout(LayoutKind.Sequential)]
	struct Counters {
		public int cb;
		public int PageFaultCount;
		public UIntPtr PeakWorkingSetSize;
		public UIntPtr WorkingSetSize;
		public UIntPtr QuotaPeakPagedPoolUsage;
		public UIntPtr QuotaPagedPoolUsage;
		public UIntPtr QuotaPeakNonPagedPoolUsage;
		public UIntPtr QuotaNonPagedPoolUsage;
		public UIntPtr PagefileUsage;
		public UIntPtr PeakPagefileUsage;
	}

	const int JobObjectBasicProcessIdList = 3;
	const int ProcessQueryLimitedInformation = 0x1000;
	static IntPtr job;
	static readonly Dictionary<long, IntPtr> handles = new Dictionary<long, IntPtr>();

	public static void Attach(IntPtr process) {
		job = CreateJobObject(IntPtr.Zero, null);
		AssignProcessToJobObject(job, process);
	}

	public static void Poll() {
		const int max = 1024;
		IntPtr list = Marshal.AllocHGlobal(16 + max * IntPtr.Size);
		try {
			if (!QueryInformationJobObject(job, JobObjectBasicProcessIdList, list, 16 + max * IntPtr.Size, IntPtr.Zero)) {
				return;
			}
			int count = Marshal.ReadInt32(list, 4);
			for (int i = 0; i < count; i++) {
				long pid = Marshal.ReadIntPtr(list, 8 + i * IntPtr.Size).ToInt64();
				if (!handles.ContainsKey(pid)) {
					handles[pid] = OpenProcess(ProcessQueryLimitedInformation, false, (int)pid);
				}
			}
		} finally {
			Marshal.FreeHGlobal(list);
		}
	}

	public static ulong Peak() {
		ulong peak = 0;
		foreach (IntPtr handle in handles.Values) {
			Counters counters;
			if (handle != IntPtr.Zero && GetProcessMemoryInfo(handle, out counters, Marshal.SizeOf(typeof(Counters)))) {
				peak = Math.Max(peak, counters.PeakWorkingSetSize.ToUInt64());
			}
		}
		return peak;
	}
}
'@

$psi = New-Object Diagnostics.ProcessStartInfo
$psi.FileName = $Command
foreach ($argument in (ConvertFrom-Json $ArgumentsJson)) {
	$psi.ArgumentList.Add($argument)
}
$psi.WorkingDirectory = (Get-Location).Path
$psi.UseShellExecute = $false
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$watch = [Diagnostics.Stopwatch]::StartNew()
$process = [Diagnostics.Process]::Start($psi)
[PeakMemory]::Attach($process.Handle)
$stdout = $process.StandardOutput.ReadToEndAsync()
$stderr = $process.StandardError.ReadToEndAsync()
while (-not $process.HasExited) {
	[PeakMemory]::Poll()
	Start-Sleep -Milliseconds 50
}
$process.WaitForExit()
$seconds = $watch.Elapsed.TotalSeconds

[IO.File]::WriteAllText($Out, $stdout.Result + $stderr.Result)
[pscustomobject]@{ seconds = $seconds; peakBytes = [PeakMemory]::Peak(); exitCode = $process.ExitCode } | ConvertTo-Json -Compress
