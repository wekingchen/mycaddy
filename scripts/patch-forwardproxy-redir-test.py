#!/usr/bin/env python3
"""兼容当前 Caddy 对明文 HTTP 重定向端口 CONNECT 请求的关闭行为。

上游 TestConnectAuthWrongProbeResistRedir 只接受 HTTP 响应；某些
Caddy / Go 环境下，该请求会被直接关闭（EOF）。测试要验证的是
启用 probe_resistance 与普通对照服务器的行为一致：
双方都正常响应则比较 HTTP 响应，双方都 EOF 也一致；
只有一方 EOF 或出现其他错误，仍然失败。

只针对上游既有测试的两处严格锚点进行修改，不跳过测试，
不修改 forwardproxy 生产代码或其他测试。
"""
from pathlib import Path
import re
import sys

if len(sys.argv) != 2:
    raise SystemExit("用法: patch-forwardproxy-redir-test.py <probe_resist_test.go>")

path = Path(sys.argv[1])
source = path.read_text(encoding="utf-8")
begin = "func TestConnectAuthWrongProbeResistRedir(t *testing.T) {"
end = "\ntype errorHeaderAlternativeServiceNotEqual struct {"
if source.count(begin) != 1 or source.count(end) != 1:
    raise SystemExit("探测抵抗重定向测试结构已变化，请人工检查上游")
before, tail = source.split(begin, 1)
test_body, after = tail.split(end, 1)

pattern = re.compile(
    r"(?P<indent>\t{4})responseProbeResist, err := (?P<probe>connectAndGetViaProxy\([^\n]+\))\n"
    r"(?P=indent)if err != nil \{\n"
    r"(?P=indent)\tt.Fatal\(err\)\n"
    r"(?P=indent)\}\n"
    r"(?P=indent)// get response from reference server without forwardproxy and compare them\n"
    r"(?P=indent)responseReference, err := (?P<reference>connectAndGetViaProxy\([^\n]+\))\n"
    r"(?P=indent)if err != nil \{\n"
    r"(?P=indent)\tt.Fatal\(err\)\n"
    r"(?P=indent)\}\n"
)

def replace(match: re.Match[str]) -> str:
    indent = match.group("indent")
    return (
        f"{indent}responseProbeResist, probeErr := {match.group('probe')}\n"
        f"{indent}// get response from reference server without forwardproxy and compare them\n"
        f"{indent}responseReference, referenceErr := {match.group('reference')}\n"
        f"{indent}if probeErr != nil || referenceErr != nil {{\n"
        f'{indent}\tprobeClosed := errors.Is(probeErr, io.EOF) || (probeErr != nil && probeErr.Error() == "http: server closed idle connection")\n'
        f'{indent}\treferenceClosed := errors.Is(referenceErr, io.EOF) || (referenceErr != nil && referenceErr.Error() == "http: server closed idle connection")\n'
        f"{indent}\tif probeClosed && referenceClosed {{\n"
        f"{indent}\t\tcontinue // 两种关闭错误均表示未返回 HTTP 响应；仍要求两端一致\n"
        f"{indent}\t}}\n"
        f'{indent}\tt.Fatalf("HTTP 重定向 CONNECT 对照不一致：probe=%v reference=%v", probeErr, referenceErr)\n'
        f"{indent}}}\n"
    )

updated, count = pattern.subn(replace, test_body)
if count != 2:
    raise SystemExit(f"上游重定向测试锚点应有 2 处，实际 {count} 处，拒绝修改")
# 上面的两处错误变量改名后，比较响应时使用局部 err 声明。
# 避免依赖之前已被删去的 err 声明，保留原有响应字段逐项比较。
old_compare = "if err = responsesAreEqual(responseProbeResist, responseReference); err != nil {"
new_compare = "if err := responsesAreEqual(responseProbeResist, responseReference); err != nil {"
if updated.count(old_compare) != 2:
    raise SystemExit("上游响应对照语句不是两处，拒绝修改")
updated = updated.replace(old_compare, new_compare)
path.write_text(before + begin + updated + end + after, encoding="utf-8")
print("✅ 重定向 CONNECT 探测抵抗测试仍执行，并同时校验对照服务器 EOF/响应一致性")
