import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from android_baseline_profile import Mapping, read_dump, to_rules, write_rules

MAPPING = '''# compiler: R8
persona.a.Kept -> persona.a.Kept:
# {"id":"sourceFile","fileName":"Kept.kt"}
    int count -> count
    1:4:void run(persona.a.Gone):10:13 -> run
    5:5:java.lang.String persona.a.Helper.name():20:20 -> run
    5:5:void run(persona.a.Gone):14:14 -> run
    1:2:java.util.List items():30:31 -> items
      # {"id":"com.android.tools.r8.residualsignature","signature":"()Ljava/util/ArrayList;"}
persona.a.Gone -> persona.a.Kept:
    1:3:void gone():40:42 -> gone
persona.a.Helper -> persona.a.Helper:
    1:1:void <init>():1:1 -> <init>
persona.a.Kept$$ExternalSyntheticLambda0 -> persona.a.Kept$$ExternalSyntheticLambda0:
# {"id":"sourceFile","fileName":"R8$$SyntheticClass"}
# {"id":"com.android.tools.r8.synthesized"}
    1:1:void <init>(int):0:0 -> <init>
      # {"id":"com.android.tools.r8.synthesized"}
    2:3:void persona.a.Helper.<init>():1:1 -> invoke
    2:3:kotlin.Unit persona.a.Kept.run$lambda$0(int):50:50 -> invoke
      # {"id":"com.android.tools.r8.residualsignature","signature":"()Ljava/lang/Object;"}
    4:6:kotlin.Unit persona.a.Kept.run$lambda$0(int):51:52 -> invoke
'''
DUMP = '''=== profile ===
base.apk [index=0] [checksum=1]
\thot methods:
\t\tvoid persona.a.Kept.run(persona.a.Kept)[],
\t\tjava.util.ArrayList persona.a.Kept.items()[],
\t\tvoid persona.a.Kept.vanished()[],
\t\tvoid androidx.Lib.go(int)[],
\tstartup methods:
\t\tvoid persona.a.Kept.run(persona.a.Kept)[],
\t\tjava.lang.Object persona.a.Kept$$ExternalSyntheticLambda0.invoke()[],
\tclasses:
\t\tpersona.a.Kept,
\t\tpersona.a.Kept$$ExternalSyntheticLambda0,
\t\tpersona.a.Kept[],
'''


class BaselineProfileTest(unittest.TestCase):
    def rules(self, dump=DUMP):
        methods, classes = {}, set()
        read_dump(dump, methods, classes)
        return to_rules(Mapping(MAPPING), methods, classes)

    def test_names_go_back_to_the_ones_r8_read(self):
        methods, classes, stats, dropped = self.rules()
        # A merged parameter type and a narrowed return type map back to the source descriptors.
        self.assertEqual({'H', 'S'}, methods[('Lpersona/a/Kept;', 'run', '(Lpersona/a/Gone;)V')])
        self.assertEqual({'H'}, methods[('Lpersona/a/Kept;', 'items', '()Ljava/util/List;')])
        # The lambda body R8 moved into a synthetic class is kept; what was inlined into it and the synthetic are not.
        self.assertEqual({'S'}, methods[('Lpersona/a/Kept;', 'run$lambda$0', '(I)Lkotlin/Unit;')])
        self.assertNotIn(('Lpersona/a/Helper;', 'name', '()Ljava/lang/String;'), methods)
        self.assertNotIn(('Lpersona/a/Helper;', '<init>', '()V'), methods)
        # Every class merged into a residual class, and the class a synthetic was made for.
        self.assertEqual({'Lpersona/a/Kept;', 'Lpersona/a/Gone;'}, classes)
        # Names the mapping does not list are not R8's and stay; a listed class's unknown method is dropped.
        self.assertIn(('Landroidx/Lib;', 'go', '(I)V'), methods)
        self.assertEqual(['Lpersona/a/Kept;->vanished()V'], dropped)
        self.assertEqual(1, stats['methods_dropped'])

    def test_rule_lines_in_art_format(self):
        methods, classes, _, _ = self.rules()
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / 'baseline-prof.txt'
            write_rules(path, methods, classes)
            lines = [line for line in path.read_text().splitlines() if not line.startswith('#')]
        self.assertIn('Lpersona/a/Gone;', lines)
        self.assertIn('HSLpersona/a/Kept;->run(Lpersona/a/Gone;)V', lines)
        self.assertFalse([line for line in lines if '**' in line or '$$External' in line])

    def test_reads_rules_as_well_as_profman_text(self):
        methods, classes, _, _ = self.rules('HSLpersona/a/Kept;->run(Lpersona/a/Kept;)V\nLpersona/a/Kept;\n')
        self.assertEqual({'H', 'S'}, methods[('Lpersona/a/Kept;', 'run', '(Lpersona/a/Gone;)V')])
        self.assertEqual({'Lpersona/a/Kept;', 'Lpersona/a/Gone;'}, classes)

    def test_cli_preserves_existing_files_when_no_rules_remain(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dump, mapping, output, report = (root / name for name in ('dump.txt', 'mapping.txt', 'profile.txt', 'report.json'))
            mapping.write_text(MAPPING)
            command = [sys.executable, '-B', str(Path(__file__).with_name('android_baseline_profile.py')), 'rules',
                       str(dump), '--mapping', str(mapping), '--out', str(output), '--report', str(report)]
            for source, extra in [('', []), ('capture unavailable\n', []), (DUMP, ['--only', 'persona.missing'])]:
                with self.subTest(source=source[:30], extra=extra):
                    dump.write_text(source)
                    output.write_text('existing profile\n')
                    report.write_text('existing report\n')
                    run = subprocess.run(command + extra, capture_output=True, text=True, timeout=10)
                    self.assertNotEqual(run.returncode, 0, run.stdout)
                    self.assertIn('no baseline profile rules', run.stderr)
                    self.assertEqual(output.read_text(), 'existing profile\n')
                    self.assertEqual(report.read_text(), 'existing report\n')

    def test_cli_accepts_class_only_and_method_only_profiles(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dump, mapping, output = (root / name for name in ('dump.txt', 'mapping.txt', 'profile.txt'))
            mapping.write_text('')
            for rule in ('Lpersona/example/Fixture;', 'HLpersona/example/Fixture;->work()V'):
                with self.subTest(rule=rule):
                    dump.write_text(rule + '\n')
                    command = [sys.executable, '-B', str(Path(__file__).with_name('android_baseline_profile.py')), 'rules',
                               str(dump), '--mapping', str(mapping), '--out', str(output)]
                    run = subprocess.run(command, capture_output=True, text=True, timeout=10)
                    self.assertEqual(run.returncode, 0, run.stderr)
                    self.assertEqual(json.loads(run.stdout)['rules'], 1)
                    self.assertEqual(output.read_text().splitlines()[-1], rule)


if __name__ == '__main__':
    unittest.main()
