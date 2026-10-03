# red-check runner for the footage command's controls.
#
# One control per run, per .kilo/skills/red-check. Each entry says the file, the
# exact bytes to remove, and the ONE test that must go red. The runner records the
# transcript, restores the file, proves the restore by hash, and re-runs the test to
# prove it is green again.
#
# It writes Go files with UTF8Encoding($false) and byte-exact replacements because
# Set-Content encodes as ANSI on this machine and corrupts an em dash into
# mojibake. Every file touched here is pure ASCII, so the round trip is exact.

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
     from="`treturn clampRegion(padded)"; to="`treturn padded"
     claim='padThenClamp clamps the padded region to the frame'
     cost='a region at the frame edge records a pad that fell outside it, the recorded geometry is wider than the pixels measured, and every composed box is misplaced' },

  @{ id='RC-32'; test='TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain'
     file='cmd/pocctl/footage_crop.go'
     from="`t`tif coveredByAny(r, accepted, p.CoveredFraction) {`n`t`t`tplan.Dedupe++`n`t`t`tcontinue`n`t`t}"; to=""
     claim='a region already inside the accepted union is dropped rather than measured again'
     cost='one object is counted as several true positives and the crop pass reports a recall it did not earn' },

  @{ id='RC-33'; test='TestComposingACropBoxIsTheInverseOfSlicingTheRegion'
     file='cmd/pocctl/footage_crop.go'
     from="X1:    (float64(b.X0) + box.X1*cw64) / fw64,"
     to="X1:    (box.X1*cw64) / fw64,"
     claim='composeBox adds the crop own pixel origin'
     cost='every crop box lands at the crop origin scaled against the frame: the right size in the wrong place, which a per-class table reports as a MISS' },

  @{ id='RC-34'; test='TestAMisnamedDetectionIsAFalsePositiveNotATruePositive'
     file='cmd/pocctl/perclass.go'
     from="`t`tfor n, a := range byClass {"
     to="`t`tfor n, a := range map[string]*acc{} {"
     claim='partitionByClass partitions detections and ground truth by class before measuring'
     cost='a `car` on a `person` box becomes a true positive, and the per-class table reports the worst failure of a detector as its best row' },

  @{ id='RC-35'; test='TestTheGateRefusesMaterialWithNoProvenanceRow'
     file='cmd/pocctl/footage.go'
     from="`tcase 0:`n`t`treturn footageLabel{}, fmt.Errorf("
     to="`tcase 0:`n`t`tif true { return footageLabel{}, nil }`n`t`treturn footageLabel{}, fmt.Errorf("
     claim='resolveLabel refuses material with no row in the provenance table'
     cost='material with nothing written about it is measured, and the refusal that would have caught it now names the wrong field' },

  @{ id='RC-36'; test='TestTheGateRefusesAnAbsentClearanceField'
     file='cmd/pocctl/footage.go'
     from="`tcase c.Cleared == nil:`n`t`twhy = append(why, "
     to="`tcase false:`n`t`twhy = append(why, "
     claim='the gate refuses a row whose cleared_for_external_use field is absent'
     cost='car-crossing.mp4 and bird-synthetic.mp4, which nobody ever cleared, are measured and reported beside material that was' },

  @{ id='RC-37'; test='TestASamplingIntervalOverOneAndAHalfSecondsIsRefused'
     file='cmd/pocctl/footage.go'
     from="`tif every > maxSamplingInterval {"
     to="`tif false && every > maxSamplingInterval {"
     claim='a sampling interval over 1.5s is refused'
     cost='a road sampled every three seconds reports an empty road over material a pedestrian was standing on' },

  @{ id='RC-38'; test='TestTheGateRefusesAnAmbiguousBaseName'
     file='cmd/pocctl/footage.go'
     from="`tdefault:`n`t`tnames := make([]string, 0, len(byBase))"
     to="`tdefault:`n`t`treturn byBase[0], nil`n`t`tnames := make([]string, 0, len(byBase))"
     claim='an ambiguous base name is refused rather than resolved'
     cost='one of two rows is measured under the other row licence, and nobody can tell which' },

  @{ id='RC-35b'; test='TestTheGateRefusesALicenceThatBeginsNotRecorded'
     file='cmd/pocctl/footage.go'
     from='} else if strings.HasPrefix(normaliseSentence(c.Licence), licenceUnrecorded) {'
     to='} else if normaliseSentence(c.Licence) == licenceUnrecorded {'
     claim='the licence check is a PREFIX test, so the one row that says NOT RECORDED in a sentence is caught'
     cost='the single most important row in the provenance table is measured, and its numbers may be quoted' },

  @{ id='RC-39'; test='TestAMeasurementOverRefusalsRendersNotRunAndNeverZero'
     file='cmd/pocctl/footage_cmd.go'
     from="`tcase r.Whole.Refusals+r.Crop.Refusals > 0:"
     to="`tcase false:"
     claim='renderNotMeasured has an arm for a measurement the decoder refused everywhere'
     cost='the section goes quiet about the one thing it exists to say, and a run of refusals reads like a run of zeros' },

  @{ id='RC-40'; test='TestARateWithNoDenominatorPrintsNotRunAndNeverZero'
     file='cmd/pocctl/perclass.go'
     from="`tif !ok {`n`t`treturn padRight(notRun, width)`n`t}`n"
     to=""
     claim='rate prints NOT RUN when the denominator is empty'
     cost='a precision of 0.000 with no detections, which claims every detection was wrong when there were none' },

  @{ id='RC-41'; test='TestTrailingBytesAreRefusedRatherThanDropped'
     file='cmd/pocctl/footage_frames.go'
     from="`tif n <= 0 {`n`t`treturn nil`n`t}"
     to="`tif true {`n`t`treturn nil`n`t}"
     claim='a stream that ends mid-frame is refused'
     cost='the last sample is silently dropped and the sample count is a lie, or a half frame is measured' }
)

$log = New-Object System.Collections.Generic.List[string]
function Say($s) { $log.Add($s); Write-Host $s }

Say("RED-CHECK TRANSCRIPTS - pocctl footage")
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
  Say("REMOVED   $($c.file):  '$($c.from.Replace("`n",'\n').Replace("`t",'\t'))'  ->  '$($c.to.Replace("`n",'\n').Replace("`t",'\t'))'")
  $out = & $go test -count=1 -run "^$($c.test)$" ./cmd/pocctl/ 2>&1 | Out-String
  $code = $LASTEXITCODE
  $firstFail = ($out -split "`n" | Where-Object { $_ -match 'FAIL|footage_test\.go' } | Select-Object -First 3) -join ' // '
  Say("EXIT      $code  (a red check that is not red is not a red check)")
  Say("TRANSCRIPT $firstFail")
  $redOk = ($code -ne 0)
  if (-not $redOk) { $failures++ }

  [System.IO.File]::WriteAllText($path, $original, $enc)
  $after = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
  $restored = ($before -eq $after)
  if (-not $restored) { Say("RESTORE   FAILED - the hash changed back"); $failures++ } else { Say("RESTORE   proved by sha256 $before") }

  $out2 = & $go test -count=1 -run "^$($c.test)$" ./cmd/pocctl/ 2>&1 | Out-String
  $code2 = $LASTEXITCODE
  Say("GREEN     exit $code2 after the restore")
  if ($code2 -ne 0) { $failures++ }
  Say("")
}

Say("checks run: $($checks.Count)   problems: $failures")
[System.IO.File]::WriteAllText($outFile, ($log -join "`r`n") + "`r`n", $enc)
Write-Host ""
Write-Host "transcripts written to $outFile"
Pop-Location
exit $failures
