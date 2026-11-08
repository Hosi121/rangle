import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'app.dart';
import 'data/api_clip_repository.dart';
import 'data/clip_repository.dart';
import 'community/community_api.dart';
import 'sharing/share_clip.dart';
import 'viewer/free_view_adapter.dart';

void main() {
  const apiUrl = String.fromEnvironment('RANGLE_API_URL');
  runApp(
    RangleApp(
      community: CommunityApi(
        endpoint: Uri.base
            .resolve(apiUrl.isEmpty ? '/api/v1/' : apiUrl)
            .resolve('./'),
      ),
      initialClipId: Uri.base.queryParameters['clip'],
      repository: apiUrl.isEmpty
          ? const DemoClipRepository(
              previewUrl: String.fromEnvironment('RANGLE_PREVIEW_URL'),
              sceneUrl: String.fromEnvironment('RANGLE_SCENE_URL'),
            )
          : ApiClipRepository(endpoint: apiUrl),
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
    this.initialClipId,
    this.shareAction = shareClip,
  });

  final ClipRepository repository;
  final ViewerFactory? viewerFactory;
  final ViewerFactory? sceneFactory;
  final CommunityApi? community;
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
        initialClipId: initialClipId,
        shareAction: shareAction,
        repository: repository,
        viewerFactory: viewerFactory ?? VideoPreviewAdapter.new,
        sceneFactory: sceneFactory ?? createFreeViewAdapter,
      ),
    );
  }
}
