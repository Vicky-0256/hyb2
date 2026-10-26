"""Compare the JS synthetic Worker fixture with original IRIS classes.
Run with scipy, scikit-learn and ViennaRNA 2.7.2 installed. Only plotting and
BAM I/O imports are omitted; the original scoring/picking classes run intact.
"""
import ast
import io
import itertools
import json
from pathlib import Path
import sys
from types import SimpleNamespace
import numpy as np
import RNA
from scipy.stats import norm, expon
from scipy.sparse import csc_matrix
from scipy.optimize import lsq_linear
from sklearn.cluster import AgglomerativeClustering
from sklearn.linear_model import Lasso

root = Path(__file__).resolve().parents[1]
namespace = dict(sys=sys, np=np, RNA=RNA, itertools=itertools, norm=norm,
                 expon=expon, csc_matrix=csc_matrix, lsq_linear=lsq_linear,
                 AgglomerativeClustering=AgglomerativeClustering, Lasso=Lasso,
                 Utils=SimpleNamespace(EPS=np.finfo(np.float32).eps, MIN_LOOP_LEN=4,
                                       VALID_BP=['AU','UA','CG','GC','GU','UG']))
for filename in ['Info_RNA.py','IRIS_Core.py']:
    source = (root/'third_party/iris'/filename).read_text()
    # Compatibility-only fixes: upstream divides by zero for <10 jobs and
    # sklearn renamed affinity to metric. Neither changes the mathematics.
    source = source.replace('(len(self.hard_constraints) // 10)', 'max(1, len(self.hard_constraints) // 10)')
    source = source.replace("affinity = 'precomputed'", "metric = 'precomputed'")
    tree=ast.parse(source)
    tree.body=[node for node in tree.body if not isinstance(node,(ast.Import,ast.ImportFrom))]
    exec(compile(tree,filename,'exec'),namespace)

fixture=json.loads(Path('/tmp/hyb2-iris-test-result.json').read_text())
rna=namespace['Info_RNA']('synthetic',fixture['sequence'])
rna.PARIS=SimpleNamespace(blocks=[((a['oneStart']-1,a['oneEnd']),(a['twoStart']-1,a['twoEnd'])) for a in fixture['rawHybArms']])
stream=io.StringIO()
scoring=namespace['Scoring'](rna,ostream=stream)
scoring.compute_PARIS_support()
generation=namespace['Generating'](rna,scoring.PARIS_support,ostream=stream)
p=fixture['parameters']
generation.generate(p['C'],p['fraction'],p['kRange'])
picking=namespace['Picking'](rna,scoring.PARIS_support,generation.candidate_structures,ostream=stream)
picking.pick(p['K'])
ensemble=picking.ensemble
assert ensemble is not None
expected=dict(zip(ensemble.structures,ensemble.normalize_alpha()))
actual={c['dotBracket']:c['weight'] for c in fixture['candidates']}
assert expected.keys()==actual.keys(), (expected,actual)
for structure,weight in expected.items():
    assert abs(weight-actual[structure])<1e-7,(weight,actual[structure])
assert abs(ensemble.get_mean_free_enery()-fixture['ensemble']['meanEnergy'])<1e-5
assert abs(ensemble.get_mean_PARIS_support(scoring.PARIS_support)-fixture['ensemble']['meanSupport'])<1e-8
Path('/tmp/hyb2-iris-reference-support.json').write_text(json.dumps(scoring.PARIS_support.ravel().tolist()))
print('iris-original-python-parity: ok (members, weights, mean energy, mean support)')
