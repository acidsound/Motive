package context

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "go/ast"
    "go/parser"
    "go/token"
    "path/filepath"
    "strings"
)

type FileObservation struct {
    Path string
    Hash string
    Bytes int
    Lines int
    Functions int
    Exported int
    AlreadyRead bool
}

type Tracker struct { files map[string]string }

func NewTracker() *Tracker { return &Tracker{files: make(map[string]string)} }

func (t *Tracker) Observe(path string, data []byte) FileObservation {
    sum := sha256.Sum256(data)
    hash := hex.EncodeToString(sum[:])
    previous, seen := t.files[path]
    obs := FileObservation{Path: path, Hash: hash, Bytes: len(data), Lines: lineCount(data), AlreadyRead: seen && previous == hash}
    t.files[path] = hash
    if strings.EqualFold(filepath.Ext(path), ".go") { obs.Functions, obs.Exported = goStructure(data) }
    return obs
}

func (o FileObservation) String() string {
    parts := []string{fmt.Sprintf("file=%s", o.Path), fmt.Sprintf("bytes=%d", o.Bytes), fmt.Sprintf("lines=%d", o.Lines), fmt.Sprintf("sha256=%s", o.Hash[:12])}
    if strings.HasSuffix(o.Path, ".go") { parts = append(parts, fmt.Sprintf("functions=%d", o.Functions), fmt.Sprintf("exports=%d", o.Exported)) }
    if o.AlreadyRead { parts = append(parts, "already_read=true", "unchanged=true") }
    return "[observation] " + strings.Join(parts, " ")
}

func lineCount(data []byte) int {
    if len(data) == 0 { return 0 }
    n := 1
    for _, b := range data { if b == '\n' { n++ } }
    return n
}

func goStructure(data []byte) (int, int) {
    f, err := parser.ParseFile(token.NewFileSet(), "file.go", data, 0)
    if err != nil { return 0, 0 }
    functions, exported := 0, 0
    for _, decl := range f.Decls {
        switch d := decl.(type) {
        case *ast.FuncDecl:
            functions++
            if d.Name.IsExported() { exported++ }
        case *ast.GenDecl:
            for _, spec := range d.Specs {
                switch s := spec.(type) {
                case *ast.TypeSpec:
                    if s.Name.IsExported() { exported++ }
                case *ast.ValueSpec:
                    for _, name := range s.Names { if name.IsExported() { exported++ } }
                }
            }
        }
    }
    return functions, exported
}
