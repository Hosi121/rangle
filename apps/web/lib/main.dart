import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'app.dart';
import 'config/app_config.dart';
import 'data/clip_repository.dart';
import 'community/community_api.dart';
import 'generation/generation_api.dart';
import 'sharing/share_clip.dart';
import 'viewer/free_view_adapter.dart';

void main() {
  const config = AppConfig.fromEnvironment();
  final communityEndpoint = config.communityEndpoint();
  runApp(
    RangleApp(
      community: communityEndpoint == null
          ? null
          : CommunityApi(endpoint: communityEndpoint),
      initialClipId: Uri.base.queryParameters['clip'],
      initialGeneration: Uri.base.queryParameters['generate'] == '1',
      repository: config.createClipRepository(),
    ),
  );
}

class RangleApp extends StatelessWidget {
  const RangleApp({
    required this.repository,
    super.key,
    this.viewerFactory,
    this.sceneFactory,
    this.community,
    this.generation,
    this.initialGeneration = false,
    this.initialClipId,
    this.shareAction = shareClip,
  });

  final ClipRepository repository;
  final ViewerFactory? viewerFactory;
  final ViewerFactory? sceneFactory;
  final CommunityApi? community;
  final GenerationApi? generation;
  final bool initialGeneration;
  final String? initialClipId;
  final ClipShareAction shareAction;

  @override
  Widget build(BuildContext context) {
    const accent = Color(0xFFB9EF7E);
    const colors = ColorScheme.dark(
      primary: accent,
      onPrimary: Color(0xFF1C2B12),
      secondary: accent,
      onSecondary: Color(0xFF1C2B12),
      surface: Color(0xFF101211),
      onSurface: Color(0xFFF0F2ED),
      onSurfaceVariant: Color(0xFFBAC1B7),
      outline: Color(0xFF71796E),
      outlineVariant: Color(0xFF30372F),
      surfaceContainer: Color(0xFF191D19),
      surfaceContainerHigh: Color(0xFF242A23),
      surfaceContainerHighest: Color(0xFF30372F),
    );
    return MaterialApp(
      title: 'rangle',
      debugShowCheckedModeBanner: false,
      locale: const Locale('ja'),
      supportedLocales: const [Locale('ja')],
      localizationsDelegates: GlobalMaterialLocalizations.delegates,
      theme: ThemeData(
        useMaterial3: true,
        colorScheme: colors,
        scaffoldBackgroundColor: colors.surface,
        visualDensity: VisualDensity.standard,
        tooltipTheme: const TooltipThemeData(waitDuration: Duration.zero),
        sliderTheme: const SliderThemeData(
          showValueIndicator: ShowValueIndicator.never,
        ),
      ),
      home: ClipFeedScreen(
        community: community,
        generation: generation,
        initialGeneration: initialGeneration,
        initialClipId: initialClipId,
        shareAction: shareAction,
        repository: repository,
        viewerFactory: viewerFactory ?? VideoPreviewAdapter.new,
        sceneFactory: sceneFactory ?? createFreeViewAdapter,
      ),
    );
  }
}
