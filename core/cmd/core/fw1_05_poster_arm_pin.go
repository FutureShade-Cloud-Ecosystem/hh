package main

// FW1-05b (S5's round 2 P3 note): the compile pin that keeps the production poster's transaction arm
// live rides an UNTAGGED file, so a plain build or vet fails the tree the day an adapter loses the
// arm, not only an integration tagged run. bankrec's one transaction path (the match and its clearing
// journal in one commit) is live only through this arm (FROM-PRINCIPAL 232 fails a poster without it
// closed, so a lost arm would refuse every match rather than orphan one, and the desk would see it).

import "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"

var _ bankrec.JournalPosterTx = glPosterAdapter{}
