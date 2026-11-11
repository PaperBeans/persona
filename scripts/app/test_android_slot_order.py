import json
import tempfile
import unittest
from pathlib import Path

from android_slot_order import SOURCES, is_function, scan, violations


class SlotOrderTest(unittest.TestCase):
    def test_function_types(self):
        for t in ('() -> Unit', '(@Composable RowScope.() -> Unit)?', '@Composable () -> Unit', 'suspend (Int) -> Unit',
                  '((String) -> Unit)?', 'LazyListScope.() -> Unit', 'Map<String, (Int) -> Unit>.() -> Unit'):
            self.assertTrue(is_function(t), t)
        for t in ('Boolean', 'String?', 'Map<String, () -> Unit>', 'List<(Int) -> Unit>?', '(() -> Unit)?.Wrapper'):
            self.assertFalse(is_function(t), t)

    def test_the_break_in_4124_fails_and_its_fix_passes(self):
        broken = '''
            @Composable
            fun JSectionHeader(
                title: String,
                onClick: (() -> Unit)? = null,
                action: (@Composable RowScope.() -> Unit)? = null,
                trailingIsFoldAction: Boolean = false,
            ) { }
        '''
        self.assertEqual([('JSectionHeader', 'trailingIsFoldAction', 'action')], violations(broken))
        fixed = broken.replace('''                action: (@Composable RowScope.() -> Unit)? = null,
                trailingIsFoldAction: Boolean = false,''', '''                trailingIsFoldAction: Boolean = false,
                action: (@Composable RowScope.() -> Unit)? = null,''')
        self.assertEqual([], violations(fixed))

    def test_what_is_not_a_slot_component_or_not_checked(self):
        source = '''
            private val Hidden = 1
            // fun JCommented(slot: @Composable () -> Unit, after: Int)
            val text = "fun JInString(slot: @Composable () -> Unit, after: Int)"
            @Composable
            private fun JPrivate(slot: @Composable () -> Unit, after: Int) { }
            fun JNotComposable(slot: @Composable () -> Unit, after: Int) { }
            @Composable
            fun JNoSlot(onClick: () -> Unit = {}, label: String) { }
            @Composable
            fun JPlainLambdaLast(slot: @Composable () -> Unit, content: LazyListScope.() -> Unit) { }
            @OptIn(ExperimentalFoundationApi::class)
            @Composable
            internal fun JAnnotated(
                values: Map<String, List<Int>> = emptyMap(),
                pick: (String, Int) -> Boolean = { _, n -> n > 0 },
                slot: @Composable () -> Unit,
            ) { }
        '''
        self.assertEqual([], violations(source))

    def test_a_declaration_before_the_annotations_is_not_the_components(self):
        # A `private val` above an annotated public component doesn't make it private.
        source = '''
            private const val LIMIT = 3

            @Composable
            fun JPublic(avatar: @Composable () -> Unit, waiting: Boolean = false) { }
        '''
        self.assertEqual([('JPublic', 'waiting', 'avatar')], violations(source))

    def test_the_baseline_may_only_shrink(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            sources = root / SOURCES / 'kotlin'
            sources.mkdir(parents=True)
            (sources / 'Rows.kt').write_text('''
                @Composable fun JRow(trailing: @Composable () -> Unit, tint: Int = 0) { }
                @Composable fun JTag(text: String, slot: @Composable () -> Unit, tone: Int) { }
                @Composable fun JTag(text: String, slot: @Composable () -> Unit) { }
            ''')
            counts, _ = scan(root)
            key = f'{SOURCES}/kotlin/Rows.kt'
            self.assertEqual({f'{key}::JRow': 1, f'{key}::JTag': 1}, counts)
            # Run as the script does: a component on the list passes, another overload of it doesn't.
            import android_slot_order as checker
            baseline = root / 'baseline.json'
            baseline.write_text(json.dumps({'version': 1, 'components': {f'{key}::JRow': 1, f'{key}::JTag': 1}}))
            self.assertEqual(0, self._run(checker, root, baseline))
            (sources / 'Rows.kt').write_text((sources / 'Rows.kt').read_text() + '''
                @Composable fun JTag(slot: @Composable () -> Unit, size: Int) { }
            ''')
            self.assertEqual(1, self._run(checker, root, baseline))

    @staticmethod
    def _run(checker, root, baseline):
        import sys
        from contextlib import redirect_stdout
        from io import StringIO
        argv = sys.argv
        sys.argv = ['android_slot_order.py', '--root', str(root), '--baseline', str(baseline)]
        try:
            with redirect_stdout(StringIO()):
                return checker.main()
        finally:
            sys.argv = argv


if __name__ == '__main__':
    unittest.main()
