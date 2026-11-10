import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

/// The barrier's DOM surface sits above the scene iframe and below the modal's
/// Flutter content. It protects both modal controls and outside-tap dismissal,
/// and disappears with the route so the scene remains interactive afterwards.
mixin _InterceptViewerBarrier<T> on ModalRoute<T> {
  @override
  Widget buildModalBarrier() =>
      PointerInterceptor(child: super.buildModalBarrier());
}

Future<T?> showViewerDialog<T>({
  required BuildContext context,
  required WidgetBuilder builder,
}) {
  final navigator = Navigator.of(context, rootNavigator: true);
  return navigator.push(
    _ViewerDialogRoute<T>(
      context: context,
      builder: builder,
      themes: InheritedTheme.capture(from: context, to: navigator.context),
      barrierColor: DialogTheme.of(context).barrierColor ?? Colors.black54,
    ),
  );
}

class _ViewerDialogRoute<T> extends DialogRoute<T>
    with _InterceptViewerBarrier<T> {
  _ViewerDialogRoute({
    required super.context,
    required super.builder,
    required super.themes,
    required super.barrierColor,
  }) : super(traversalEdgeBehavior: TraversalEdgeBehavior.closedLoop);
}

Future<T?> showViewerBottomSheet<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  BoxConstraints? constraints,
  Color? backgroundColor,
  Clip? clipBehavior,
}) {
  final navigator = Navigator.of(context);
  final localizations = MaterialLocalizations.of(context);
  return navigator.push(
    _ViewerBottomSheetRoute<T>(
      builder: builder,
      capturedThemes: InheritedTheme.capture(
        from: context,
        to: navigator.context,
      ),
      constraints: constraints,
      backgroundColor: backgroundColor,
      clipBehavior: clipBehavior,
      barrierLabel: localizations.scrimLabel,
      barrierOnTapHint: localizations.scrimOnTapHint(
        localizations.bottomSheetLabel,
      ),
      modalBarrierColor: Theme.of(context).bottomSheetTheme.modalBarrierColor,
    ),
  );
}

class _ViewerBottomSheetRoute<T> extends ModalBottomSheetRoute<T>
    with _InterceptViewerBarrier<T> {
  _ViewerBottomSheetRoute({
    required super.builder,
    required super.capturedThemes,
    required super.constraints,
    required super.backgroundColor,
    required super.clipBehavior,
    required super.barrierLabel,
    required super.barrierOnTapHint,
    required super.modalBarrierColor,
  }) : super(isScrollControlled: true, useSafeArea: true);
}
