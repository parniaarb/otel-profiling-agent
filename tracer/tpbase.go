/*
 * Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
 * or more contributor license agreements. Licensed under the Apache License 2.0.
 * See the file "LICENSE" for details.
 */

package tracer

import (
	"encoding/hex"
	"errors"
	"fmt"
	"unsafe"

	cebpf "github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"

	"github.com/elastic/otel-profiling-agent/libpf/rlimit"
	"github.com/elastic/otel-profiling-agent/support"
	"github.com/elastic/otel-profiling-agent/tpbase"

	log "github.com/sirupsen/logrus"

	"github.com/elastic/otel-profiling-agent/libpf"
)

// This file contains code to extract the offset of the thread pointer base variable in
// the `task_struct` kernel struct, which is needed by e.g. Python and Perl tracers.
// This offset varies depending on kernel configuration, so we have to learn it dynamically
// at run time.
//
// Unfortunately, /dev/kmem is often disabled for security reasons, so a BPF helper is used to
// read the kernel memory in portable manner. This code is then analyzed to get the data.
//
// If you're wondering how to check the disassembly of a kernel function:
// 1) Extract your vmlinuz image (the extract-vmlinux script is in the Linux kernel source tree)
//    linux/scripts/extract-vmlinux /boot/vmlinuz-5.6.11 > kernel.elf
// 2) Find the address of aout_dump_debugregs in the ELF
//    address=$(cat /boot/System.map-5.6.11 | grep "T aout_dump_debugregs" | awk '{print $1}')
// 3) Disassemble the kernel ELF starting at that address:
//    objdump -S --start-address=0x$address kernel.elf | head -20

// loadKernelCode will request the ebpf code read the first X bytes from given address.
func loadKernelCode(coll *cebpf.CollectionSpec, maps map[string]*cebpf.Map,
	functionAddress libpf.SymbolValue) ([]byte, error) {
	funcAddressMap := maps["codedump_addr"]
	functionCode := maps["codedump_code"]

	key0 := uint32(0)
	funcAddr := uint64(functionAddress)

	if err := funcAddressMap.Update(unsafe.Pointer(&key0), unsafe.Pointer(&funcAddr),
		cebpf.UpdateAny); err != nil {
		return nil, fmt.Errorf("failed to write codedump_addr 0x%x: %v",
			functionAddress, err)
	}

	restoreRlimit, err := rlimit.MaximizeMemlock()
	if err != nil {
		return nil, fmt.Errorf("failed to adjust rlimit: %v", err)
	}
	defer restoreRlimit()

	// Load a BPF program to load the function code in functionCode.
	// Trigger it via a sys_enter_bpf tracepoint so we can easily ensure the code is run at
	// least once before we read the map for the result. Hacky? Maybe...
	prog, err := cebpf.NewProgram(coll.Programs["tracepoint__sys_enter_bpf"])
	if err != nil {
		return nil, fmt.Errorf("failed to load tracepoint__sys_enter_bpf: %v", err)
	}
	defer prog.Close()

	perfEvent, err := link.Tracepoint("syscalls", "sys_enter_bpf", prog, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to configure tracepoint: %v", err)
	}
	defer perfEvent.Close()

	codeDump := make([]byte, support.CodedumpBytes)

	if err := functionCode.Lookup(unsafe.Pointer(&key0), &codeDump); err != nil {
		return nil, fmt.Errorf("failed to get codedump: %v", err)
	}

	// Make sure the map is cleared for reuse.
	value0 := uint32(0)
	if err := functionCode.Update(unsafe.Pointer(&key0), unsafe.Pointer(&value0),
		cebpf.UpdateAny); err != nil {
		return nil, fmt.Errorf("failed to delete element from codedump_code: %v", err)
	}

	return codeDump, nil
}

// loadTPBaseOffset extracts the offset of the thread pointer base variable in the `task_struct`
// kernel struct. This offset varies depending on kernel configuration, so we have to learn
// it dynamically at runtime.
func loadTPBaseOffset(coll *cebpf.CollectionSpec, maps map[string]*cebpf.Map,
	kernelSymbols *libpf.SymbolMap) (uint64, error) {
	var tpbaseOffset uint32
	for _, analyzer := range tpbase.GetAnalyzers() {
		sym, err := kernelSymbols.LookupSymbol(libpf.SymbolName(analyzer.FunctionName))
		if err != nil {
			continue
		}

		code, err := loadKernelCode(coll, maps, sym.Address)
		if err != nil {
			return 0, err
		}

		tpbaseOffset, err = analyzer.Analyze(code)
		if err != nil {
			return 0, fmt.Errorf("%w: %s", err, hex.Dump(code))
		}
		log.Infof("Found tpbase offset: %v (via %s)", tpbaseOffset, analyzer.FunctionName)
		break
	}

	if tpbaseOffset == 0 {
		return 0, errors.New("no supported symbol found")
	}

	// Sanity-check against reasonable values. We expect something in the ~2000-10000 range,
	// but allow for some additional slack on top of that.
	if tpbaseOffset < 500 || tpbaseOffset > 20000 {
		return 0, fmt.Errorf("tpbase offset %v doesn't look sane", tpbaseOffset)
	}

	return uint64(tpbaseOffset), nil
}
