#!/usr/bin/env python3
import argparse, json, statistics, time, urllib.request, urllib.error, sys, pprint
from pathlib import Path

HERE = Path(__file__).resolve().parent

class HTTPFailure(RuntimeError):
    pass

def post(url, obj, timeout):
    body = json.dumps(obj, ensure_ascii=False).encode()
    req = urllib.request.Request(url, data=body, headers={'Content-Type':'application/json'}, method='POST')
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.load(r)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='replace')
        raise HTTPFailure(f'HTTP {exc.code}: {detail}') from exc

def root(v1):
    v1 = v1.rstrip('/')
    return v1[:-3] if v1.endswith('/v1') else v1

def tok(v1, text, timeout):
    try:
        d = post(root(v1) + '/tokenize', {'content': text}, timeout)
        return len(d['tokens']) if isinstance(d.get('tokens'), list) else None
    except Exception:
        return None

def fit(v1, seed, target, timeout):
    seed = seed.strip() + '\n'
    n = tok(v1, seed, timeout)
    if not n:
        return seed * max(1, target * 4 // max(1, len(seed))), None
    text = seed * max(1, target // n)
    n2 = tok(v1, text, timeout)
    if n2 is None:
        return text, None
    while n2 < target:
        c = text + seed
        nc = tok(v1, c, timeout)
        if nc is None or nc > target:
            break
        text, n2 = c, nc
    return text, n2

def info(resp):
    msg = ((resp.get('choices') or [{}])[0].get('message') or {})
    calls = msg.get('tool_calls') or []
    reason = msg.get('reasoning_content') or msg.get('reasoning') or ''
    usage = resp.get('usage') or {}
    return {
        'kind': 'tool_call' if calls else 'text',
        'calls': len(calls),
        'completion': usage.get('completion_tokens'),
        'reasoning_bytes': len(reason.encode()) if isinstance(reason, str) else 0,
        'bytes': len(json.dumps(resp, ensure_ascii=False).encode()),
        'message': msg,
    }

def call(v1, model, msgs, tools=None, choice=None, out=64, temp=0.0, timeout=300):
    p = {'model': model, 'messages': msgs, 'max_tokens': out, 'temperature': temp}
    if tools is not None: p['tools'] = tools
    if choice is not None: p['tool_choice'] = choice
    t = time.perf_counter()
    r = post(v1.rstrip('/') + '/chat/completions', p, timeout)
    return time.perf_counter()-t, r

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--url', default='http://127.0.0.1:8080/v1')
    ap.add_argument('--model', required=True)
    ap.add_argument('--tokens', nargs='+', type=int, default=[8000,10000,12000])
    ap.add_argument('--runs', type=int, default=3)
    ap.add_argument('--warmup', type=int, default=1)
    ap.add_argument('--output-tokens', type=int, default=64)
    ap.add_argument('--timeout', type=float, default=300)
    ap.add_argument('--dump-roundtrip', action='store_true')
    args = ap.parse_args()

    seed = (HERE/'benchmark_prompt.txt').read_text(encoding='utf-8')
    reasoning = (HERE/'benchmark_reasoning_prompt.txt').read_text(encoding='utf-8')
    tools = json.loads((HERE/'benchmark_tools.json').read_text(encoding='utf-8'))
    tool_result = (HERE/'benchmark_tool_result.txt').read_text(encoding='utf-8').strip()
    cases = ['plain','reasoning','tools','required_tool','roundtrip']
    med = {c:{} for c in cases}
    bad = []
    print('case,target_tokens,actual_tokens,run,latency_s,completion_tokens,response_kind,tool_calls,reasoning_bytes,response_bytes,status')

    for target in args.tokens:
        prompt, actual = fit(args.url, seed, target, args.timeout)
        for case in cases:
            vals=[]
            for _ in range(args.warmup):
                try:
                    _run(case,args,prompt,reasoning,tools,tool_result,dump=False)
                except Exception:
                    pass
            for run in range(1,args.runs+1):
                try:
                    elapsed, resp, status = _run(case,args,prompt,reasoning,tools,tool_result,dump=args.dump_roundtrip)
                except Exception as exc:
                    bad.append((case,target,f'exception:{type(exc).__name__}:{exc}'))
                    print(f'{case},{target},{actual if actual is not None else "?"},{run},ERROR,?,?,?,?,exception:{type(exc).__name__}', flush=True)
                    if args.dump_roundtrip and case == 'roundtrip':
                        print(f'[roundtrip:error] {type(exc).__name__}: {exc}', file=sys.stderr)
                    continue
                x=info(resp); vals.append(elapsed); med[case].setdefault(target,[]).append(elapsed)
                if status != 'ok': bad.append((case,target,status))
                print(f"{case},{target},{actual if actual is not None else '?'},{run},{elapsed:.3f},{x['completion'] if isinstance(x['completion'],int) else '?'},{x['kind']},{x['calls']},{x['reasoning_bytes']},{x['bytes']},{status}")
            if vals:
                print(f"# {case} {target}: median={statistics.median(vals):.3f}s min={min(vals):.3f}s max={max(vals):.3f}s")

    print('\n=== diagnosis ===')
    target = args.tokens[-1]
    for c in cases:
        v=med[c].get(target)
        if v: print(f'{c:14s} {statistics.median(v):.3f}s')
    p=statistics.median(med['plain'][target]) if target in med['plain'] else None
    r=statistics.median(med['reasoning'][target]) if target in med['reasoning'] else None
    t=statistics.median(med['tools'][target]) if target in med['tools'] else None
    tc=statistics.median(med['required_tool'][target]) if target in med['required_tool'] else None
    rt=statistics.median(med['roundtrip'][target]) if target in med['roundtrip'] else None
    if bad: print('WARNING: ' + str(bad))
    if p is not None and r is not None and r > 5*p and r > 5:
        print('DIAGNOSIS: reasoning workload is the dominant multiplier.')
    elif p is not None and t is not None and t > 3*p and t > 3:
        print('DIAGNOSIS: tool schema overhead is a major multiplier.')
    elif p is not None and tc is not None and not bad and tc > 3*p and tc > 3:
        print('DIAGNOSIS: required tool generation is a major multiplier.')
    elif p is not None and rt is not None and rt > 5*p and rt > 5:
        print('DIAGNOSIS: multi-turn context is the dominant multiplier.')
    else:
        print('DIAGNOSIS: no single layer dominates by >5x; compare Motive request semantics next.')

def _run(case,args,prompt,reasoning,tools,tool_result,dump=False):
    system={'role':'system','content':'You are a benchmark participant. Follow the requested task.'}
    if case=='plain':
        msgs=[system,{'role':'user','content':prompt+'\nReply briefly.'}]
        e,r=call(args.url,args.model,msgs,out=args.output_tokens,timeout=args.timeout); return e,r,'ok'
    if case=='reasoning':
        msgs=[system,{'role':'user','content':prompt+'\n'+reasoning}]
        e,r=call(args.url,args.model,msgs,out=args.output_tokens,timeout=args.timeout); return e,r,'ok'
    if case=='tools':
        msgs=[system,{'role':'user','content':prompt+'\nDo not use any tools. Reply briefly.'}]
        e,r=call(args.url,args.model,msgs,tools=tools,out=args.output_tokens,timeout=args.timeout); return e,r,'ok'
    if case in ('required_tool','roundtrip'):
        msgs=[system,{'role':'user','content':prompt+'\nCall read_file exactly once for README.md.'}]
        e1,r1=call(args.url,args.model,msgs,tools=tools,choice='required',out=args.output_tokens,timeout=args.timeout)
        i1=info(r1)
        if i1['calls']==0:
            return e1,r1,'no_tool_call'
        if case=='required_tool':
            return e1,r1,'ok'
        m=dict(i1['message'])
        calls=m.get('tool_calls') or []
        if not calls:
            return e1,r1,'no_tool_call'
        # Replay exactly the assistant tool-call turn, making content explicit.
        m['content'] = m.get('content') or ''
        msgs.append(m)
        c=calls[0]
        msgs.append({'role':'tool','tool_call_id':c.get('id','benchmark'),'content':tool_result})
        if dump:
            print('\n[roundtrip:first_response]', file=sys.stderr)
            pprint.pp(r1, stream=sys.stderr, width=140)
            print('\n[roundtrip:second_request]', file=sys.stderr)
            pprint.pp({'model':args.model,'messages':msgs,'tools':tools,'max_tokens':args.output_tokens,'temperature':0.0}, stream=sys.stderr, width=140)
        e2,r2=call(args.url,args.model,msgs,tools=tools,out=args.output_tokens,timeout=args.timeout)
        r2['_first_latency']=e1
        r2['_second_latency']=e2
        return e1+e2,r2,'ok'
    raise ValueError(case)

if __name__=='__main__':
    main()
