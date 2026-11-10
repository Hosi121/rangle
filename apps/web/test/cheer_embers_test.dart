import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/widgets/cheer_embers.dart';

void main() {
  testWidgets(
    'rapid cheers overlap without blocking taps and settle completely',
    (tester) async {
      var trigger = 0, taps = 0;
      late StateSetter update;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Center(
              child: StatefulBuilder(
                builder: (context, setState) {
                  update = setState;
                  return CheerEmbers(
                    trigger: trigger,
                    child: IconButton(
                      tooltip: '声援',
                      onPressed: () {
                        taps++;
                        update(() => trigger++);
                      },
                      icon: const Icon(Icons.local_fire_department),
                    ),
                  );
                },
              ),
            ),
          ),
        ),
      );
      expect(find.byKey(const Key('cheer-embers')), findsNothing);
      for (var i = 0; i < 12; i++) {
        await tester.tap(find.byTooltip('声援'));
        await tester.pump(const Duration(milliseconds: 30));
      }
      expect(taps, 12);
      expect(find.byKey(const Key('cheer-embers')), findsOneWidget);
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('cheer-embers')), findsNothing);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets('reduced motion keeps the action but omits flying particles', (
    tester,
  ) async {
    Widget view(int trigger) => MaterialApp(
      home: MediaQuery(
        data: const MediaQueryData(disableAnimations: true),
        child: CheerEmbers(
          trigger: trigger,
          child: const SizedBox(width: 48, height: 48),
        ),
      ),
    );
    await tester.pumpWidget(view(0));
    await tester.pumpWidget(view(1));
    expect(find.byKey(const Key('cheer-embers')), findsNothing);
    await tester.pumpAndSettle();
  });
}
