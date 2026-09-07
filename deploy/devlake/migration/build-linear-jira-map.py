# Author: Navjyot Nishant
# Created: 2026-09-02
# Description: Verified Linear->Jira issue-key mapping by normalised title match.
import re, collections

DASHES = str.maketrans({'�': '-', '—': '-', '–': '-'})

def norm(t):
    return re.sub(r'\s+', ' ', t.strip().translate(DASHES)).casefold()

def load(p):
    rows = []
    for line in open(p, encoding='utf-8', errors='replace'):
        line = line.rstrip('\n')
        if not line: continue
        k, t = line.split('\t', 1)
        rows.append((k, t))
    return rows

linear = load('/tmp/linear-issues.tsv')
jira = load('/tmp/jira-map/jira-issues.tsv')

fffd = sum(1 for _, t in linear if '�' in t)
emdash = sum(1 for _, t in linear if '—' in t)
print(f"Linear titles containing U+FFFD: {fffd}")
print(f"Linear titles containing em dash: {emdash}")
print(f"Jira titles containing U+FFFD: {sum(1 for _,t in jira if chr(0xfffd) in t)}")

jindex = collections.defaultdict(list)
for k, t in jira:
    jindex[norm(t)].append(k)

num = lambda k: int(k.split('-')[1])

unique, ambiguous, unmatched = [], [], []
for lk, lt in linear:
    cands = jindex.get(norm(lt), [])
    if len(cands) == 1:
        unique.append((lk, cands[0], lt))
    elif len(cands) > 1:
        ambiguous.append((lk, cands, lt))
    else:
        unmatched.append((lk, lt))

print(f"\ntotal Linear: {len(linear)}")
print(f"total Jira: {len(jira)}")
print(f"unique matches: {len(unique)}")
print(f"ambiguous: {len(ambiguous)}")
print(f"unmatched: {len(unmatched)}")

offs = [(num(lk) - num(jk), lk, jk) for lk, jk, _ in unique]
dist = collections.Counter(o for o, _, _ in offs)
print("\noffset distribution (offset: count):")
for o, c in dist.most_common():
    print(f"  {o}: {c}")

print("\noffset ranges (by linear key order):")
byl = sorted(offs, key=lambda x: num(x[1]))
start = byl[0]
prev = byl[0]
for cur in byl[1:] + [None]:
    if cur is None or cur[0] != prev[0]:
        print(f"  offset {prev[0]}: {start[1]}..{prev[1]}  (jira {start[2]}..{prev[2]})")
        if cur: start = cur
    if cur: prev = cur
print(f"\nCONSTANT: {len(dist) == 1}")

print("\nambiguous:")
for lk, cands, lt in ambiguous:
    print(f"  {lk}\t{lt}\t-> {', '.join(cands)}")

print("\nunmatched:")
for lk, lt in unmatched:
    print(f"  {lk}\t{lt}")

with open('/tmp/jira-map/mapping.tsv', 'w', encoding='utf-8') as f:
    for lk, jk, lt in sorted(unique, key=lambda x: num(x[0])):
        f.write(f"{lk}\t{jk}\t{lt}\n")
print(f"\nmapping.tsv rows: {len(unique)}")

# check: no jira key used twice
dup = [k for k, c in collections.Counter(jk for _, jk, _ in unique).items() if c > 1]
assert not dup, f"jira key reused: {dup}"
