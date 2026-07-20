-- Harness init for the vibe review surfaces: registers the review plugin
-- (A/R verdicts + ✓/✗ badge linemode). scripts/review.sh appends the
-- project's own init.lua after this when one exists.
require("vibe"):setup()
