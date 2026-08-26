package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/acidsound/Motive/internal/runtime"
	"github.com/acidsound/Motive/internal/session"
)

type unitRecord struct { SessionID string; Boundary runtime.UnitBoundary }
func (m *model) loadUnits() []unitRecord { var out []unitRecord; if m.sess==nil{return out}; summaries,err:=m.sess.List(); if err!=nil{return out}; for _,sum:=range summaries { entries,err:=m.sess.Load(sum.ID); if err!=nil{continue}; for _,e:=range entries { if e.Role!="unit"{continue}; var b runtime.UnitBoundary; if json.Unmarshal([]byte(e.Content),&b)!=nil { b=runtime.UnitBoundary{Status:"unknown",Error:e.Content} }; out=append(out,unitRecord{SessionID:sum.ID,Boundary:b}) } }; return out }
func unitLines(units []unitRecord,cursor int) []string { if len(units)==0{return []string{styleDim.Render("no unit executions recorded")}}; out:=[]string{stylePanelHeading.Render(fmt.Sprintf("Unit executions (%d)",len(units)))}; for i,u:=range units { b:=u.Boundary; marker:="   "; if i==cursor{marker=stylePrompt.Render(" ▸ ")}; line:=fmt.Sprintf(" %s %s · %s · steps %d/%d · tools %d",marker,shortID8(u.SessionID),statusStyle(b.Status).Render(b.Status),b.Steps,b.MaxSteps,b.ToolCalls); if b.BaseRevision!=""||b.ResultRevision!=""{line+=" · Δrev "+shortRev(b.BaseRevision)+".."+shortRev(b.ResultRevision)}; if b.Error!=""{line+="\n    "+styleError.Render(truncateRunes(firstLine(b.Error,90),90))} else if t:=strings.TrimSpace(b.Text); t!=""{line+="\n    "+styleDim.Render(truncateRunes(firstLine(t,90),90))}; out=append(out,line) }; return out }
func shortID8(id string) string { if len(id)>15{return id[:15]}; return id }
func firstLine(s string,limit int) string { s=strings.TrimSpace(s); if i:=strings.IndexByte(s,'\n');i>=0{s=s[:i]}; s=strings.TrimSpace(s); r:=[]rune(s); if len(r)>limit{return string(r[:limit])+"…"}; return s }
func statusStyle(status string) (s lipgloss.Style) { switch status { case "completed":return styleOK; case "budget-exceeded":return styleEffort; default:return styleError } }
func formatUnitDetail(entries []session.Entry) string { var b strings.Builder; for _,e:=range entries { switch e.Role { case "user":b.WriteString(styleUser.Render("❯ "+firstLine(e.Content,200))); case "assistant":b.WriteString(styleAssistant.Render(firstLine(e.Content,200))); case "unit":b.WriteString(styleBookmark.Render("⏹ boundary: "+e.Content)); default:b.WriteString(styleDim.Render(e.Role+": "+firstLine(e.Content,120))) }; b.WriteString("\n") }; return b.String() }
var _=strconv.Itoa
