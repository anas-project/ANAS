#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 PROCESSGROUP_TEST JOBEXECUTOR_TEST RUNNER_TEST HTTPAPI_TEST" >&2
  exit 2
fi

processgroup_test=$1
jobexecutor_test=$2
runner_test=$3
httpapi_test=$4

for binary in "$processgroup_test" "$jobexecutor_test" "$runner_test" "$httpapi_test"; do
  if [[ ! -x "$binary" ]]; then
    echo "test binary is missing or not executable: $binary" >&2
    exit 2
  fi
done

"$processgroup_test" -test.v -test.count=1 -test.run '^TestCancellationTerminatesWholeProcessGroupAfterGrace$'
"$jobexecutor_test" -test.v -test.count=1 -test.run '^TestExecutorCancellationTerminatesLifecycleProcessGroupAndCompensates$'
"$runner_test" -test.v -test.count=1 -test.run '^TestWorkspaceLifecyclePreviewExpandsFrozenDependenciesAndRejectsDrift$'
"$httpapi_test" -test.v -test.count=1 -test.run '^(TestLifecyclePreviewMustConfirmRunnerExpandedChainBeforeJobCreation|TestRollbackRequiresExplicitTargetAndExactImpactConfirmation)$'

echo "CONSOLE-R-031 PASS typed lifecycle execution used application services"
echo "CONSOLE-R-034 PASS start/stop/restart/rollback job contracts accepted exact previews"
echo "CONSOLE-R-057 PASS process group TERM/grace/KILL cancellation completed compensation"
echo "CONSOLE-R-124 PASS runner-expanded Module chain was required for job creation"
