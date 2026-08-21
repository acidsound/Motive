# Motive 설계 근거 (Design Rationale)

> **이 문서는 "왜 이렇게 만들었는가"를 설명합니다.**  
> `stable-semantics.md`가 *무엇이 안정적인 의미인가*를 기록한다면, 이 문서는 그 의미를 선택한 *이유*와 *맥락*을 기록합니다.  
> 분류: **[RATIONALE]** — 설계 결정의 근거. 소스 코드나 테스트가 아닌 프로젝트 판단을 반영합니다.
>
> English version: [docs/design-rationale.md](design-rationale.md)

---

## 1. Motive는 무엇인가

Motive는 **모델 중심(model-centric) 소프트웨어 실행 런타임**입니다.

- **모델이 추론과 계획을 수행**합니다.
- **Motive는 워크스페이스, 도구, 실행, 개정(revision) 추적을 제공**합니다.

이것이 전부입니다. 에이전트 프레임워크, 플러그인 시스템, 플래너(planner) 레이어, 서브에이전트(sub-agent), 메모리 매니저는 **의도적으로** 포함하지 않습니다.

---

## 2. 실행 루프 — 어떻게 작동하는가

```
사용자 요청
  → 컨텍스트 컴파일 (ContextBlock: 시스템 프롬프트 + 워크스페이스 + Git 상태)
  → OpenAI 호환 모델 (/v1/chat/completions)
  → 모델 응답 (도구 호출 포함 가능)
  → 도구 실행 → 결과를 메시지 목록에 추가
  → 런타임 관측(runtime observation) 추가
  → (반복: 모델 → 도구 → 관측)
  → 도구 없는 응답 → 최종 응답으로 반환
```

**[SOURCE: internal/runtime/runtime.go, Execute 메서드]**

이 루프의 각 반복을 **스텝(step)**이라고 합니다.  
스텝, 소요 시간, 도구 호출 수는 **실행 예산(execution budget)**으로 제한됩니다.  
예산을 초과하면 실행이 중단됩니다.

---

## 3. 왜 Stateless 인가

### 3.1 핵심 불변 조건

> 각 사용자 요청은 **신선한 모델 컨텍스트(fresh model context)**로 시작됩니다.  
> Motive는 실행 요청을 처리하기 위해 **보이지 않는 채팅 기록에 의존하지 않습니다.**

**[SOURCE: stable-semantics.md §3, runtime.go Execute — 매 호출마다 messages 슬라이스를 새로 구성]**

### 3.2 이유

**영속적 세계(Persistent World)는 채팅 기록이 아니라 워크스페이스입니다.**

에이전트 프레임워크는 보통 대화 기록(context history)을 영속 상태로 취급합니다.  
Motive는 반대입니다: **파일과 Git 상태가 진짜 영속 상태이고, 모델 컨텍스트는 일회성 작업 공간입니다.**

```
전통적 에이전트:  컨텍스트 = 메모리 / 요약 / 기록
Motive:           컨텍스트 = 일회용 작업대, 워크스페이스 = 영속 저장소
```

### 3.3 장점

| 장점 | 설명 |
|------|------|
| **재현성(Reproducibility)** | 같은 요청 → 같은 초기 컨텍스트. 이전 실행의 오염(contamination)이 없음. |
| **결정성(Determinism)** | Git HEAD와 워크스페이스 상태가 같으면 초기 컨텍스트가 항상 동일. |
| **컨텍스트 오염 방지** | 이전 턴의 오류, 중복, 모순된 지시가 없음. 각 실행은 자체 논리로 시작. |
| **상태 충돌 불존재** | 런타임이 보지 못한 숨김 상태 없음. 크래시 후 재시작 = 완전히 신선한 시작. |
| **확장성** | 여러 실행이 컨텍스트 충돌 염려 없이 병행 실행 가능. |

### 3.4 예시: 세션 복구

세션 JSONL 트랜스크립트는 **컨텍스트가 아니라 기록**입니다.  
중단된 실행은 `session_log` 도구로 트랜스크립트의 꼬리를 읽고 모델 스스로 복구합니다.  
런타임은 인메모리 상태를 가지고 있지 않으며, 복구를 강제하지 않습니다.

---

## 4. 맥락(Context)은 어떻게 파악하는가

### 4.1 컨텍스트 컴파일 (ContextBlock)

`Runtime.ContextBlock()`은 다음 정보로 초기 시스템 메시지를 구성합니다:

```
system: You are Motive, ...
Workspace: <워크스페이스 루트 경로>
Git HEAD: <현재 커밋 해시>
Git status: <git status --short --branch 출력>
Workspace files: <파일 목록 (최대 6000바이트, .git/node_modules 제외)>
Session: <세션 ID>  (TUI 실행 시에만)
```

**[SOURCE: runtime.go ContextBlock]**

### 4.2 파일 목록의 크기 제한

워크스페이스 파일 목록은 6000바이트로 제한(truncate)됩니다.  
이는 모델이 컨텍스트를 "무엇이 있는가" 파악할 수 있을 정도로 유지하면서,  
거대한 저장소에서 컨텍스트가 폭발하는 것을 막습니다.

**[SOURCE: runtime.go truncateUTF8]**

### 4.3 모델이 직접 검사

초기 컨텍스트는 전체 워크스페이스 내용을 포함하지 않습니다.  
모델은 필요할 때 `read_file`, `glob`, `search_files`, `list_files` 도구로 직접 검사합니다.

**[SOURCE: stable-semantics.md §3]**

---

## 5. 왜 이렇게 작성했는가

### 5.1 의도적 단순성

Motive는 **의도적으로** 다음을 포함하지 않습니다:

- **에이전트 프레임워크** — LangChain, CrewAI, AutoGen 등
- **플러그인 시스템**
- **플래너 레이어**
- **서브에이전트**
- **메모리 매니저** — RAG, 벡터 스토어, 요약 모듈

이 결정의 근거:
1. **모델 자체가 가장 좋은 플래너입니다.**  
   추가 플래너 레이어를 넣는 것은 모델의 추론 능력을 제한하고 의존성을 추가합니다.

2. **레이어가 많을수록 장애 지점이 늘어납니다.**  
   각 레이어는 자체적인 오류 모드, 지연, 컨텍스트 오염을 도입합니다.

3. **Motive는 실행 환경입니다, 추상 계층이 아닙니다.**  
   모델에 접근한 파일, 셸, 웹, Git 도구를 제공하는 것이 목적입니다.

### 5.2 모델 중심 아키텍처

```
┌─────────────────────────────────────────┐
│              모델 (계획, 추론)          │
│  tool call ──► 실행 ──► 관측 ──► 다음  │
└─────────────────────────────────────────┘
│           Motive (런타임)              │
│  워크스페이스 | 셸 | 웹 | Git         │
└─────────────────────────────────────────┘
```

모델은 도구를 호출하고, Motive는 실행합니다.  
Motive는 "모델이 다음에 무엇을 할지" 판단하지 않습니다.  
판단은 전적으로 모델의 몫입니다.

### 5.3 Git은 영속성의 중추

워크스페이스 + Git은:
- **초기 컨텍스트의 기준점** (Git HEAD)
- **변경 사항의 영속 기록** (base revision → result revision)
- **실행 간 조정 매체** (workspace + revision delta)

---

## 6. 왜 Compaction을 하지 않는가

### 6.1 문제: Compaction은 근본 해결책이 아니다

컨텍스트 트리밍(trimming)이나 컴팩션(compaction)은  
**한 실행(execution)의 천장(ceiling)을 높일 뿐**입니다.

```
Track A (context lifecycle):   한 컨텍스트의 수명을 연장
Track B (decomposition):       여러 독립 실행으로 분할
```

Motive는 **Track B**를 선택했습니다.  
그 이유는 EPIC(큰 작업)이 아무리 잘 압축된 한 컨텍스트에도 맞지 않기 때문입니다.

**[SOURCE: docs/decomposition.md §2]**

### 6.2 Compaction의 구체적 문제점

1. **숨은 추론 내용 노출 위험**  
   컨텍스트를 요약/압축하면 모델의 reasoning이 손실되거나 왜곡될 위험이 있습니다.  
   Motive는 `[execution-state]` 관측만 모델에 보이고,  
   reasoning content는 분리되어 표시되며 압축하지 않습니다.

2. **"무엇이 잘렸는가"의 재현성 손실**  
   컴팩션은 비가역적(irreversible)입니다.  
   트랜스크립트의 원본 메시지는 남지만, 모델이 본 컨텍스트는 재구성할 수 없습니다.

3. **Compaction 자체가 또 다른 문제 도메인**  
   어떤 메시지를 유지/제거할지, 어떻게 요약할지 결정하는 것은  
   모델 수준의 판단이 필요하며, 이는 "모델이 판단한다"는 원칙과 충돌합니다.

### 6.3 대안: Fresh Context per Unit

대형 작업은 **여러 신선한 컨텍스트**로 분할합니다. 각 유닛은:
- 자체 실행 예산
- 자체 타임아웃
- 자체 Git revision 범위
- 자체 세션

유닛 간 조정은 워크스페이스 + Git delta를 통해 이루어집니다.  
이것이 `docs/decomposition.md`의 핵심입니다.

### 6.4 Fresh 재판단이 Stale 요약보다 낫다

컴팩션은 과거 컨텍스트를 요약해서 현재 모델이 "요약된 과거"를 봅니다.  
Fresh context 방법은 유닛의 `brief.md` + Git diff를 보고 **신선하게** 재판단합니다.

> **요약은 요약자의 편향을 담고 있습니다.  
> 신선한 재판단은 원본 증거(brief + diff)를 직접 봅니다.**

---

## 7. Motive만의 고유한 시스템

### 7.1 세션 = 트랜스크립트, 컨텍스트가 아님

- **세션은 JSONL 파일**입니다.  
- 사용자/어시스턴트/오류/중단(stopped) 항목을 추가만 합니다.  
- 모델 컨텍스트를 **저장하지 않습니다.** 이전 실행의 메시지는 복원되지 않습니다.
- **[SOURCE: internal/session/session.go]**

세션 로그는 모델이 읽을 수 있습니다:
- `session_log` 도구 → 세션의 꼬리를 읽음
- `motive` 도구 → Motive 자체 작동 지침

이것이 **복구 메커니즘**입니다: 이전 실행이 중단된 곳을 트랜스크립트에서 읽고 계속합니다.

### 7.2 런타임 관측 (Runtime Observation)

도구 호출 후, Runtime은 `[execution-state]` 메시지를 모델 컨텍스트에 추가합니다:

```
[execution-state]
step=12/64 tools=34/128 failures=0 context=45231 peak=89210
elapsed=2m30s effort=low rev=abc1234→def5678
```

관측은 **3-4줄**로 압축되어 있으며,  
모델이 예산 압박, 실패, 컨텍스트 성장을 인식할 수 있게 합니다.

**[SOURCE: runtime.go Observation.Format]**

### 7.3 Context Accounting

Runtime은 각 스텝 전에 컨텍스트 추정 토큰 수를 계산합니다:
- bytes/4 휴리스틱 (모델 클라이언트와 동일)
- 최대/최고 추정 추적
- 서버 제공 prompt_n 분리 기록
- 설정된 최대를 초과하면 Overflow 보고

**[SOURCE: runtime.go ContextAccounting]**

### 7.4 Reasoning Effort

`low` / `medium` / `high` / `xhigh` / `max` 5단계.  
기본값은 `low`. 도구 실패 시 일시적으로 `xhigh`로 상승 후 복귀.

**[SOURCE: model/client.go normalizeEffort, runtime.go toolFailed 브랜치]**

### 7.5 Git Revision 기록

모든 실행은 시작 시점의 `base_revision`과 종료 시점의 `result_revision`을 기록합니다.  
이는 TraceEvent, session Entry, runtime observation 모두에 포함됩니다.

### 7.6 Steer / Queue 정책

실행 중에도 사용자가 개입할 수 있습니다:
- **Steer**: 현재 실행 중인 컨텍스트에 메시지 주입
- **Queue**: 현재 실행 종료 후 처리할 FIFO

**[SOURCE: runtime.go takeSteer, README.md Steer/queue policy]**

### 7.7 바운디드 Execution (Bounded Execution)

| 자원 | 기본값 | 최대 상한 |
|------|--------|-----------|
| 최대 스텝 | 64 | 256 |
| 최대 소요 시간 | 30분 | 120분 |
| 최대 도구 호출 | 128 | 1024 |

안전 경계(safety boundary)로, 모델 추론 예산이 아닙니다.  
**[SOURCE: config.go, runtime.go]**

### 7.8 분해(Decomposition) — 구현 완료 (Form 0)

대형 작업은 **분해(decomposition)**를 통해 처리됩니다: 하나의 EPIC을 여러 독립적이고 재조합 가능한
바운디드 실행(bounded execution)으로 분할합니다. 이는 **데이터**로 표현된 모델 행동입니다
(`motive.tasks/plan.md` + 유닛별 `brief.md`). 런타임 플래너나 서브에이전트가 아닙니다.
유닛은 기존 `shell` 도구를 통해 자체 신선한 컨텍스트에서 실행됩니다.
런타임은 모든 종료 경로에서 기계적인 **`UnitBoundary`** 레코드(상태, `base_rev → result_rev`,
예산 사용량)를 작성하며, one-shot CLI는 각 유닛에 자체 세션 트랜스크립트를 부여하여 복구를 지원합니다.

분해는 의도적으로 오류 가능성을 내포합니다 — 잘못된 분할은 경계 이벤트(`budget-exceeded` / `error` /
조립 불가능한 diff)로 표면화되며, `plan.md`를 다시 작성하여 수정합니다. `plan.md`는 가설(hypothesis)이지
계약(contract)이 아닙니다. 상위(parent)는 저렴한 단계(brief 작성 → 유닛 실행 → 결과 읽기 → 다음 brief 작성)만
수행하고, 무거운 작업은 자체 예산을 가진 신선한 컨텍스트 유닛 내부에서 실행됩니다.

**[SOURCE: docs/decomposition.md; internal/runtime/runtime.go UnitBoundary; cmd/motive/main.go]**

---

## 8. 장점 요약

### 8.1 기존 에이전트 프레임워크 vs Motive

| 항목 | 전통적 에이전트 (LangChain, CrewAI 등) | Motive |
|------|----------------------------------------|--------|
| **상태 관리** | 컨텍스트 히스토리 + 메모리 모듈 | 워크스페이스 + Git만이 영속 |
| **세션 영속성** | 대화 기록 저장/복원 | JSONL 트랜스크립트 (컨텍스트 불포함) |
| **계획** | 플래너 체인/레이어 | 모델이 자체 추론으로 계획 |
| **도구** | 플러그인/툴킷 체계 | 고정된 14개 도구 (직접적, 구체적) |
| **Context 관리** | 윈도우/요약/압축 | Fresh per execution (압축 없음) |
| **실행 예산** | 제한 없거나 별도 설정 | 스텝/시간/도구 호출 3중 안전 장치 |
| **실행 격리** | 대개 불명확 (숨김) | Git rev range로 명시 |
| **복구** | 세션 복원으로 컨텍스트 복구 | 모델이 session_log 읽고 자체 복구 |
| **확장** | 모듈/체인/에이전트 추가 | EPIC decomposition (fresh context per unit) |

### 8.2 Motive만의 강점

1. **모델이 최상의 플래너** — 추가 계층 없이 모델의 추론 능력을 최대한 활용.
2. **투명성** — Git revision, session transcript, trace events로 모든 실행이 기록됨.
3. **재현 가능한 실행** — 동일 워크스페이스 + Git HEAD → 동일 초기 컨텍스트.
4. **안전한 실행 경계** — step/time/tool 3중 예산으로 모델 실행 보호.
5. **실패 회복력** — 도구 실패는 종료가 아닌 정보; xhigh 상승으로 대응; session_log로 복구.
6. **런타임 관측** — 모델이 자체 실행 상태를 인식 (context pressure, budget usage, failure rate).
7. **사용자 개입** — steer/queue로 실행 중에도 방향 조정 가능.

---

## 9. 앞으로의 방향 (Frontier)

분해(Form 0)는 **구현 완료**되었습니다(§7.8). 남은 것은 보류/범위 외 작업입니다:

- **Track A — 컨텍스트 생명주기(context lifecycle)**: 단일 실행의 트리밍/컴팩션은
  아직 범위 외(`stable-semantics.md` §23).
- **자율 정책 자체 수정(autonomous policy self-modification)**
  (`stable-semantics.md` §20 항목 5): 이후의 별도 프론티어.
- **일반화된 다중 유닛 오케스트레이션**: 경계 기계(boundary machinery)는 검증되었지만,
  여러 유닛에 걸친 신뢰할 수 있는 대규모 분해는 아직 한 실험에서 입증된 모델 스킬일 뿐,
  런타임 보장이 아닙니다.
  **[SOURCE: docs/decomposition.md §12]**

---

## 부록: 설계 결정 요약

| 결정 | 이유 | 증거 |
|------|------|------|
| **Stateless context** | 재현성, 오염 방지, 확장성 | runtime.go Execute |
| **Workspace + Git = 영속 상태** | 파일과 revision이 유일한 진짜 상태 | stable-semantics.md §3 |
| **No agent framework** | 모델이 최고의 플래너, 추가 계층은 장애 지점 | runtime.go, systemPrompt |
| **No compaction** | Track B (fresh context per unit)가 근원 해결 | docs/decomposition.md §2 |
| **Runtime observation** | 모델이 자신의 실행을 인식해야 최적화 가능 | runtime.go Observation.Format |
| **Session = transcript** | 컨텍스트 대신 기록, 재구성보다 재판단 | session.go |
| **Bounded execution** | 안전 경계, 모델의 추론 예산과 분리 | runtime.go, config.go |
| **Tool failure ≠ termination** | 모델이 회복할 기회 제공 | runtime.go toolFailed |
| **xhigh escalation** | 실패 후 집중 추론 유도 | runtime.go effort 스위칭 |
| **분해(Decomposition) (Form 0)** | 하나의 EPIC = 여러 신선한 컨텍스트 바운디드 유닛; 데이터 기반(`brief.md`), 런타임 플래너 아님 | docs/decomposition.md, runtime.go UnitBoundary |