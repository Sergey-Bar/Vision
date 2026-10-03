# red-check runner for `pocctl footage`.
#
# One control per run, per .kilo/skills/red-check. Each entry names the file, the exact
# bytes to remove, and the ONE test that must go red. The runner records the transcript,
# restores the file, proves the restore by SHA-256, and re-runs the test to prove it is
# green again.
#
# SCOPE AFTER THE MERGE. This branch used to own six more controls. Four of them moved
# upstream with the code they guarded and are guarded there now:
#
#   crop-to-frame geometry  -> internal/ortdetect/crop.go and crop_test.go
#   per-class partition     -> internal/bench/perclass.go and perclass_test.go
#   gate: no provenance row -> footage.go, TestMaterialWithNoRowAtAllIsRefused
#   gate: ambiguous lookup  -> footage.go, TestAnAmbiguousLookupIsRefusedRatherThanGuessed
#   gate: absent clearance  -> footage.go, TestAnAbsentClearedFlagIsRefused
#
# Keeping them here would have meant keeping the code they guard, which is the duplicate
# the merge removed. footage_redcheck.md says the same in prose.
#
# It writes Go files with UTF8Encoding($false) and byte-exact replacements because
# Set-Content encodes as ANSI on this machine and corrupts an em dash into mojibake.
# Every file touched here is pure ASCII except footage.go, whose existing content carries
# upstream's own em dashes; a byte-exact round trip preserves them unchanged, which the
# SHA-256 restore proves.

$ErrorActionPreference = 'Stop'
$go = "C:\Program Files\Go\bin\go.exe"
$enc = New-Object System.Text.UTF8Encoding($false)
# The module root, two levels up from this script. Paths in the table below are
# module-root-relative, so the runner works from any working directory.
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$outFile = Join-Path $root 'cmd\pocctl\footage_redcheck_transcripts.txt'

$checks = @(
  @{ id='RC-31'; test='TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame'
     file='cmd/pocctl/footage_crop.go'
     from="`tx0 := clampInt(int(math.Round(padded.X1*float64(frameW))), 0, frameW)"
     to="`tx0 := int(math.Round(padded.X1 * float64(frameW)))"
     claim='padThenClamp CLAMPS the padded region to the frame, and clamps before it returns'
     cost='a region at the frame edge records a pad that fell outside it, `Rect.Clamp` then disagrees with the rectangle we recorded, and every box the detector maps back onto the frame is placed against a crop it never saw'
     red='the rectangle comes back with X below zero, and the test also fails its Clamp-agrees check' },

  @{ id='RC-32'; test='TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain'
     file='cmd/pocctl/footage_crop.go'
     from="`t`tif coveredByAny(r, accepted, p.CoveredFraction) {`n`t`t`tplan.Dedupe++`n`t`t`tcontinue`n`t`t}"
     to=""
     claim='a region already inside the accepted union is dropped rather than measured again'
     cost='novelty returns several components per moving object; measuring a component already inside an accepted one counts a single object as several true positives and the crop pass reports a recall it did not earn'
     red='two regions survive where one belongs' },

  @{ id='RC-35b'; test='TestALicenceReadingNotRecordedIsRefusedOnItsOwnStopsFiring'
     file='cmd/pocctl/footage.go'
     from='} else if strings.HasPrefix(upper(licence), licenceUnrecorded) {'
     to='} else if upper(licence) == licenceUnrecorded {'
     claim='the licence check is a PREFIX test, so the one row that says NOT RECORDED inside a sentence is caught'
     cost='the single most important row in the provenance table is measured, and a row whose licence says nothing was recorded while its clearance field says yes is measured and quoted. This is the only place this branch deliberately changed an upstream line'
     red='a row with an unrecorded licence and a cleared flag is accepted; upstream own test passes today only because that row clearance field is false, so a SECOND check catches it by accident' },

  @{ id='RC-39'; test='TestAMeasurementOverRefusalsRendersNotRunAndNeverZero'
     file='cmd/pocctl/footage_measure.go'
     from="`tcase r.Whole.Refusals+r.Crop.Refusals > 0:"
     to="`tcase false:"
     claim='renderNotMeasured has an arm for a measurement the decoder refused everywhere'
     cost='the section goes quiet about the one thing it exists to say, and on this export that arm IS the result: a run of refusals would read like a run of zeros'
     red='the report stops saying UNKNOWN, which is the honest result' },

  @{ id='RC-40'; test='TestARateWithNoDenominatorPrintsNotRunAndNeverZero'
     file='cmd/pocctl/footage_sidebyside.go'
     from="`trec := notRun"
     to="`trec := `"0.000`""
     claim='a rate with an empty denominator prints NOT RUN rather than a zero'
     cost='a recall of zero over no drawn boxes claims nothing was found when nothing was there to find; 0.000 is the most reassuring thing a table can print and the one most likely to be wrong'
     red='the kite row loses its NOT RUN recall cell and prints 0.000' },

  @{ id='RC-41'; test='TestTrailingBytesAreRefusedRatherThanDropped'
     file='cmd/pocctl/footage_frames.go'
     from="`tif n <= 0 {`n`t`treturn nil`n`t}"
     to="`tif true {`n`t`treturn nil`n`t}"
     claim='a stream that ends mid-frame is refused'
     cost='the last sample is silently dropped and the sample count is a lie, or a half frame is measured and the number describes a picture nobody decoded'
     red='a stream that ended mid-frame is accepted' }
)

$log = New-Object System.Collections.Generic.List[string]
function Say($s) { $log.Add($s); Write-Host $s }

Say("RED-CHECK TRANSCRIPTS - pocctl footage, after the merge with contract")
Say("machine: $env:COMPUTERNAME   module: poc/active-monitoring")
Say("")

$failures = 0
Push-Location $root
foreach ($c in $checks) {
  $path = Join-Path $root $c.file
  $original = [System.IO.File]::ReadAllText($path)
  $before = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
  if ($original.IndexOf($c.from) -lt 0) {
    Say("=== $($c.id) SKIPPED: the exact text to remove was not found in $($c.file)")
    Say("    searched for: $($c.from.Replace("`n", '\n').Replace("`t", '\t'))")
    $failures++
    continue
  }
  $mutated = $original.Replace($c.from, $c.to)
  [System.IO.File]::WriteAllText($path, $mutated, $enc)

  Say("=== $($c.id)  $($c.test)")
  Say("CLAIM     $($c.claim)")
  Say("REMOVED   $($c.file)")
  Say("            '$($c.from.Replace("`n",'\n').Replace("`t",'\t'))'  ->  '$($c.to.Replace("`n",'\n').Replace("`t",'\t'))'")
  Say("COST      $($c.cost)")
  $out = & $go test -count=1 -run "^$($c.test)$" ./cmd/pocctl/ 2>&1 | Out-String
  $code = $LASTEXITCODE
  $firstFail = ($out -split "`n" | Where-Object { $_ -match 'FAIL|footage_test\.go|footage_measure_test\.go' } | Select-Object -First 3) -join ' // '
  Say("EXIT      $code  (a red check that is not red is not a red check)")
  Say("RED AS    $($c.red)")
  Say("TRANSCRIPT $firstFail")
  if ($code -eq 0) { $failures++ }

  [System.IO.File]::WriteAllText($path, $original, $enc)
  $after = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
  if ($before -ne $after) {
    Say("RESTORE   FAILED - the hash did not come back")
    $failures++
  } else {
    Say("RESTORE   proved by sha256 $before")
  }

  $out2 = & $go test -count=1 -run "^$($c.test)$" ./cmd/pocctl/ 2>&1 | Out-String
  $code2 = $LASTEXITCODE
  Say("GREEN     exit $code2 after the restore")
  if ($code2 -ne 0) { $failures++ }
  Say("")
}
Pop-Location

Say("checks run: $($checks.Count)   problems: $failures")
Say("controls moved upstream with their code and guarded there: crop-to-frame geometry")
Say("(internal/ortdetect/crop_test.go), per-class partition (internal/bench/perclass_test.go),")
Say("gate no-row / ambiguous-lookup / absent-clearance (footage_test.go, upstream's own tests).")
[System.IO.File]::WriteAllText($outFile, ($log -join "`r`n") + "`r`n", $enc)
Write-Host ""
Write-Host "transcripts written to $outFile"
exit $failures
