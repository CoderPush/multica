# NanoHome agent setup

NanoHome uses explicit agent model settings and two workspace skills. Keep reusable
procedure in skills, role ownership in agent instructions, and acceptance/evidence
in issues. Squad instructions alone do not reach specialists.

## Sources and assignments

| Skill | Workspace skill ID | Assigned agents |
| --- | --- | --- |
| [nanohome-delivery](skills/nanohome-delivery/SKILL.md) | `0ec72507-6cff-4452-bf9e-126f7d6fc9ac` | Lead, Frontend, Backend, QA, Platform, Mika |
| [nanohome-advisory-audit](skills/nanohome-advisory-audit/SKILL.md) | `465c68eb-815f-4017-91d7-860861010b92` | Mika |

Workspace: `147a98b9-f410-4908-911e-49d8bb8b791a` (`nano`).
Project: `64915d3d-db5e-4061-850a-08b7447f6ff7` (Nanohome.vn).
Squad: `05386b62-e4d8-4701-88aa-0453a8c5fe63` (Nanohome Development).

Lead and Mika use `gpt-6-luna` / `high`; all four specialists use `gpt-6-sol` /
`medium`. All select `default` service explicitly. This preserves the existing
model policy. Do not substitute an interactive CLI's model catalogue for the
actual isolated role's execution evidence.

Lead alone allocates two executable pipelines and one task per specialist. The
delivery skill defines intake, implementation, independent review, rework,
release and verified acceptance, including next-owner evidence at each handoff.
Mika remains outside the squad and cannot use the delivery skill to acquire
execution authority. Independent QA never reviews its own implementation.

## Existing automation

- Delivery `9c60c180-4fda-4450-ad48-96d389a9c033`: Lead, Run Only, hourly
  08:00–20:00 Asia/Ho_Chi_Minh.
- Advisory audit `cb3b67dd-3a5d-4d7b-bfea-657e352dbeb9`: Mika, Run Only,
  daily 09:15 Asia/Ho_Chi_Minh.

Both descriptions point to the assigned skills. Schedules, subscribers, runtime
bindings, access and model settings were preserved. No reconciliation was
triggered for the setup change, and no issue comment or Lark message was posted.

## Updating and verifying

Use the authenticated Multica CLI with this workspace ID. Update each skill's
content and its relative reference files in place, then read back with
`skill get <id> --with-content`. Compare content exactly with these sources and
verify each agent using `agent skills list <id>`. Use `agent skills add`, not a
replacement assignment that could remove other skills. Refresh settings before
editing and preserve concurrent changes. Never copy credentials into the repo.

On 23 September 2026, exact skill/reference content, all six assignments, explicit
models, runtime IDs, concurrency and both trigger schedules were verified.
Duplicated agent instructions fell from 5,062 to 539 words, excluding separately
loaded skills. This is a prompt-size measurement, not measured token/cost savings.
Skill use on an ordinary delivery and subsequent handoff behavior remain to be
observed; saved configuration does not prove model execution.

NANO-2's accumulated 14,730-word description was replaced with a 240-word queue
brief using `--no-start`, preserving its owner/status and explicitly labeling
the queue as the last recorded handoff. The complete old description is retained
as issue attachment `01a0cca3-cdb3-7932-9ebd-37279aff310b`, linked from the brief;
its downloaded content was verified against the snapshot. Comments were unchanged.

Before-change agent/squad/autopilot settings are retained locally under
`~/.multica/backups/nano-skills-20260923T115652/`, with restricted permissions.
For rollback, compare current settings with that snapshot, restore only this
change's instruction fields, and remove only these new skill assignments if
needed. Preserve intervening user edits and existing schedules/models/access.
