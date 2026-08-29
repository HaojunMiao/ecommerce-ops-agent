#!/usr/bin/env python3
"""运行可复现的 RAG 消融实验，结果由脚本直接写入 evals/results。"""
from pathlib import Path
import runpy

ROOT = Path(__file__).resolve().parents[1]
runpy.run_path(str(ROOT / "scripts" / "rag-eval" / "run.py"), run_name="__main__")
