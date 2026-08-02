// corpus-loader drives the official technical corpus import:
//
//	1. load + shape-validate the manifest (corpus/sources.yaml)
//	2. per source: pinning/license gate -> staged checkout at the immutable commit
//	3. deterministic pilot sampling (--pilot N per source) or full import
//	4. idempotent checkpointing so re-runs skip already-processed documents
//	5. hand each selected document to the RAG server upload pipeline
//
// The loader never deletes anything, never writes API keys, and refuses to
// proceed when any source fails the pinning/license gate.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"code-agent/internal/corpus"
)

func main() {
	var (
		manifestPath = flag.String("manifest", "corpus/sources.yaml", "path to the corpus manifest")
		stagingDir   = flag.String("staging", "corpus/staging", "directory for checked-out sources (gitignored)")
		checkpoint   = flag.String("checkpoint", "corpus/staging/checkpoints.txt", "checkpoint file for idempotent re-runs")
		pilot        = flag.Int("pilot", 0, "import only this many documents per source (0 = full import)")
		sourceFilter = flag.String("source", "", "import only this source_id (empty = all)")
		dryRun       = flag.Bool("dry-run", false, "validate and list documents without importing")
	)
	flag.Parse()

	ctx := context.Background()
	manifest, err := corpus.LoadManifest(*manifestPath)
	if err != nil {
		log.Fatalf("manifest: %v", err)
	}

	// Gate every source before touching the network or the pipeline.
	var blocked []string
	for _, source := range manifest.Sources {
		if err := corpus.ValidateSourceGate(source.SourceGate()); err != nil {
			blocked = append(blocked, err.Error())
		}
	}
	if len(blocked) > 0 {
		fmt.Fprintln(os.Stderr, "BLOCKED sources (pin commits and license hashes before import):")
		for _, reason := range blocked {
			fmt.Fprintln(os.Stderr, "  -", reason)
		}
		os.Exit(1)
	}

	store, err := corpus.NewFileCheckpointStore(*checkpoint)
	if err != nil {
		log.Fatalf("checkpoint store: %v", err)
	}
	defer store.Close()

	selector := corpus.PilotSelector{PerSource: *pilot}
	stager := &corpus.Stager{}
	totalSelected, totalSkipped := 0, 0

	for _, source := range manifest.Sources {
		if *sourceFilter != "" && source.SourceID != *sourceFilter {
			continue
		}
		fmt.Printf("source %s (commit %s): staging...\n", source.SourceID, source.SourceCommit)
		result, err := stager.Stage(ctx, source, *stagingDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "source %s stage FAILED: %v\n", source.SourceID, err)
			os.Exit(1)
		}
		fmt.Printf("source %s: HEAD=%s license=%s\n", source.SourceID, result.HeadCommit, result.LicensePath)

		docs, err := corpus.ListDocuments(ctx, source, filepath.Join(*stagingDir, source.SourceID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "source %s list FAILED: %v\n", source.SourceID, err)
			os.Exit(1)
		}
		keys := make([]string, 0, len(docs))
		for _, doc := range docs {
			keys = append(keys, doc.Checkpoint.Key())
		}
		pilotKeys := selector.Select(keys)

		selected, skipped := 0, 0
		for _, doc := range docs {
			if *pilot > 0 && !corpus.Contains(pilotKeys, doc.Checkpoint.Key()) {
				continue
			}
			seen, err := store.Seen(doc.Checkpoint.Key())
			if err != nil {
				log.Fatalf("checkpoint: %v", err)
			}
			if seen {
				skipped++
				continue
			}
			if *dryRun {
				fmt.Printf("  [dry-run] %s %s\n", source.SourceID, doc.SourcePath)
				selected++
				continue
			}
			// Hand off to the RAG server upload pipeline.
			if err := corpus.IngestDocument(ctx, source, doc, *stagingDir); err != nil {
				fmt.Fprintf(os.Stderr, "source %s document %s ingest FAILED: %v\n", source.SourceID, doc.SourcePath, err)
				os.Exit(1)
			}
			if err := store.Mark(doc.Checkpoint.Key()); err != nil {
				log.Fatalf("checkpoint mark: %v", err)
			}
			selected++
		}
		fmt.Printf("source %s: %d selected, %d skipped (already imported)\n", source.SourceID, selected, skipped)
		totalSelected += selected
		totalSkipped += skipped
	}
	fmt.Printf("loader done: %d documents selected, %d skipped via checkpoint\n", totalSelected, totalSkipped)
}
