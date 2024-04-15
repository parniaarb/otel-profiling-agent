/*
 * Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
 * or more contributor license agreements. Licensed under the Apache License 2.0.
 * See the file "LICENSE" for details.
 */

package interpreter

import (
	"fmt"

	"github.com/elastic/otel-profiling-agent/host"
	"github.com/elastic/otel-profiling-agent/libpf"
	"github.com/elastic/otel-profiling-agent/libpf/pfelf"
)

// LoaderInfo contains information about an ELF that is passed to
// the interpreter loaders.
type LoaderInfo struct {
	// fileID is the FileID of the ELF file.
	fileID host.FileID
	// elfRef provides a cached access to the ELF file.
	elfRef *pfelf.Reference
	// gaps represents holes in the stack deltas of the executable.
	gaps []libpf.Range
}

// NewLoaderInfo returns a populated LoaderInfo struct.
func NewLoaderInfo(fileID host.FileID, elfRef *pfelf.Reference, gaps []libpf.Range) *LoaderInfo {
	return &LoaderInfo{
		fileID: fileID,
		elfRef: elfRef,
		gaps:   gaps,
	}
}

// GetELF returns and caches a *pfelf.File for this LoaderInfo.
func (i *LoaderInfo) GetELF() (*pfelf.File, error) {
	return i.elfRef.GetELF()
}

// GetSymbolAsRanges returns the normalized virtual address ranges for the named symbol
func (i *LoaderInfo) GetSymbolAsRanges(symbol libpf.SymbolName) ([]libpf.Range, error) {
	ef, err := i.GetELF()
	if err != nil {
		return nil, err
	}
	sym, err := ef.LookupSymbol(symbol)
	if err != nil {
		return nil, fmt.Errorf("symbol '%v' not found: %w", symbol, err)
	}
	start := uint64(sym.Address)
	return []libpf.Range{{
		Start: start,
		End:   start + uint64(sym.Size)},
	}, nil
}

// FileID returns the fileID  element of the LoaderInfo struct.
func (i *LoaderInfo) FileID() host.FileID {
	return i.fileID
}

// FileName returns the fileName  element of the LoaderInfo struct.
func (i *LoaderInfo) FileName() string {
	return i.elfRef.FileName()
}

// Gaps returns the gaps for the executable of this LoaderInfo.
func (i *LoaderInfo) Gaps() []libpf.Range {
	return i.gaps
}
