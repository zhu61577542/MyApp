param(
    [ValidateRange(-1, 9)][int]$Case = -1,
    [switch]$CheckOnly
)

$ErrorActionPreference = 'Stop'
$acceptanceRoot = [IO.Path]::GetFullPath($PSScriptRoot)
if (!$acceptanceRoot.StartsWith('E:\test\', [StringComparison]::OrdinalIgnoreCase)) {
    throw '请将验收包解压到 E:\test 内再运行。'
}
$binary = Join-Path $acceptanceRoot 'ownership-stage1.test.exe'
if (!(Test-Path -LiteralPath $binary)) { throw '缺少阶段1验收程序。' }
if ($CheckOnly) { Write-Output '验收入口与 E:\test 路径检查通过。'; return }

$utf8 = New-Object Text.UTF8Encoding($false)
$logRoot = Join-Path $acceptanceRoot 'logs'
$tempRoot = Join-Path $acceptanceRoot 'temp'
[void][IO.Directory]::CreateDirectory($logRoot)
[void][IO.Directory]::CreateDirectory($tempRoot)
$env:TEMP = $tempRoot
$env:TMP = $tempRoot
try { [Console]::OutputEncoding = $utf8 } catch { }
$OutputEncoding = $utf8
$cases = @('Mac发起', 'Windows发起', '同时申请', '本地接管', '急停与恢复', '断线与超时', '迟到消息', '取消申请')
$journal = Join-Path $logRoot 'operations.jsonl'

function Invoke-StageCase([int]$number) {
    $started = [DateTimeOffset]::Now
    $stamp = $started.ToString('yyyyMMdd-HHmmss-fff') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 6)
    $stdoutPath = Join-Path $logRoot "$stamp-case$number.stdout.log"
    $stderrPath = Join-Path $logRoot "$stamp-case$number.stderr.log"
    $pattern = if ($number -eq 9) { '^TestStageOneAcceptance$' } else { '^TestStageOneAcceptance$/' + $number.ToString('00') + '_' }
    $label = if ($number -eq 9) { '全部八项后台场景' } else { $cases[$number - 1] }
    Write-Host "正在核查：$label（后台规则，不操作实体键鼠）"
    $begin = [ordered]@{time=$started.ToString('o'); type='scenario_started'; runId=$stamp; scenario=$number; label=$label}
    [IO.File]::AppendAllText($journal, (($begin | ConvertTo-Json -Compress) + [Environment]::NewLine), $utf8)
    $process = Start-Process -FilePath $binary -ArgumentList @('-test.v', '-test.timeout=30s', "-test.run=$pattern") -WorkingDirectory $acceptanceRoot -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -PassThru
    $null = $process.Handle
    $timedOut = !$process.WaitForExit(45000)
    if ($timedOut) { $process.Kill(); $process.WaitForExit() }
    $process.Refresh()
    $text = [IO.File]::ReadAllText($stdoutPath)
    $errors = [IO.File]::ReadAllText($stderrPath)
    $passed = !$timedOut -and $process.ExitCode -eq 0 -and $text -match '(?m)^PASS\r?$' -and $text -match '--- PASS: TestStageOneAcceptance/'
    $entry = [ordered]@{time=([DateTimeOffset]::Now).ToString('o'); type='scenario_finished'; runId=$stamp; scenario=$number; label=$label; passed=$passed; exitCode=$process.ExitCode; timedOut=$timedOut; durationMs=([DateTimeOffset]::Now-$started).TotalMilliseconds; stdout=[IO.Path]::GetFileName($stdoutPath); stderr=[IO.Path]::GetFileName($stderrPath)}
    [IO.File]::AppendAllText($journal, (($entry | ConvertTo-Json -Compress) + [Environment]::NewLine), $utf8)
    Write-Host $text
    if ($errors) { Write-Host $errors }
    if (!$passed) { throw "场景 $number 未通过，请保留日志并回报。" }
    Write-Host "已记录：$journal"
}

Write-Host '第一阶段：控制权状态机验收。当前桌面程序仍为0.1.9；本工具不会启用实体鼠标反向控制。'
Write-Host '只记录场景选择、时间与测试结果，不捕获按键、剪贴板或屏幕。'
if ($Case -ge 1) { Invoke-StageCase $Case; return }
while ($true) {
    for ($index = 0; $index -lt $cases.Count; $index++) { Write-Host ("{0}. {1}" -f ($index + 1), $cases[$index]) }
    Write-Host '9. 全部后台场景；0. 退出'
    $choice = Read-Host '选择要核查的编号'
    if ($choice -eq '0') { break }
    if ($choice -notmatch '^[1-9]$') { Write-Host '请输入0到9。'; continue }
    Invoke-StageCase ([int]$choice)
}
