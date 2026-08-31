# LCD transfer protocol probe

ARAM can collect a bounded, review-only report from the mapped parallel-panel
transport while original firmware runs. The probe is disabled by default
because it observes every MMIO access during the diagnostic run.

```go
machine, err := systemmachine.New(firmware, systemmachine.Options{
	ProbePanelProtocol: true,
})
if err != nil {
	return err
}

machine.Run(ctx, budget)
report, ok := machine.PanelProtocolReport()
if !ok {
	return fmt.Errorf("panel protocol probe is unavailable")
}
return json.NewEncoder(output).Encode(report)
```

`report.Candidates` correlates logical panel writes with their physical MMIO
accesses. Each candidate contains:

- command and data addresses suitable for review as
  `BoardProfile.PanelPorts`;
- observed command and data transfer widths;
- parameter and pixel packing;
- the likely RGB format and whether it came from an explicit DCS `0x3A`
  command or a weaker width-based inference;
- confidence, reason labels, and bounded evidence counters.

High confidence requires distinct 16-bit command/data ports, complete DCS
column and page windows, consistent explicit RGB565 format writes, at least
four memory-write values, and lossless logical-to-physical correlation.
Near-matches remain in the report at low confidence with
`insufficient-evidence` status. Multiple equally supported port pairs are
reported as `ambiguous`.

The report retains no arbitrary command parameters, pixels, firmware bytes,
paths, or guest memory. It does not alter or automatically register a board
profile.

## Scope

The live probe validates and characterizes a panel transport already mapped by
the selected board. An access to a wholly unmapped address faults before a
device can produce the logical command/data event needed for safe correlation,
so discovery of previously unknown apertures still requires a separately
bounded hardware-research mapping. A candidate also remains subject to
firmware review and physical-device validation.
