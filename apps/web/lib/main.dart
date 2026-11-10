import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'app.dart';
import 'config/app_config.dart';
import 'data/clip_repository.dart';
import 'community/community_api.dart';
import 'generation/generation_api.dart';
import 'publishers/publisher_api.dart';
import 'live/live_api.dart';
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
      live: config.createLiveApi(),
      initialLiveId: Uri.base.queryParameters['live'],
      initialClipId: Uri.base.queryParameters['clip'],
      initialGeneration: Uri.base.queryParameters['generate'] == '1',
      initialPublisher: Uri.base.queryParameters['publisher'] == '1',
      initialPublicPublisherId: Uri.base.queryParameters['organization'],
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
    this.initialPublisher = false,
    this.initialPublicPublisherId,
    this.publisher,
    this.initialClipId,
    this.live,
    this.liveFactory,
    this.initialLiveId,
    this.shareAction = shareClip,
  });

  final ClipRepository repository;
  final ViewerFactory? viewerFactory;
  final ViewerFactory? sceneFactory;
  final CommunityApi? community;
  final GenerationApi? generation;
  final bool initialGeneration;
  final bool initialPublisher;
  final String? initialPublicPublisherId;
  final PublisherApi? publisher;
  final String? initialClipId;
  final LiveApi? live;
  final ViewerFactory? liveFactory;
  final String? initialLiveId;
  final ClipShareAction shareAction;

  @override
  Widget build(BuildContext context) {
    const accent = Color(0xFFFF6500);
    const colors = ColorScheme.dark(
      primary: accent,
      onPrimary: Color(0xFF190C05),
      secondary: accent,
      onSecondary: Color(0xFF190C05),
      surface: Color(0xFF101010),
      onSurface: Color(0xFFF0F2ED),
      onSurfaceVariant: Color(0xFFC7C0BA),
      outline: Color(0xFF7A716A),
      outlineVariant: Color(0xFF39312C),
      surfaceContainer: Color(0xFF191614),
      surfaceContainerHigh: Color(0xFF25201D),
      surfaceContainerHighest: Color(0xFF332B26),
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
        cardTheme: CardThemeData(
          elevation: 0,
          color: colors.surfaceContainer,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(20),
            side: BorderSide(color: colors.outlineVariant),
          ),
        ),
        inputDecorationTheme: InputDecorationTheme(
          filled: true,
          fillColor: colors.surfaceContainer,
          border: OutlineInputBorder(borderRadius: BorderRadius.circular(16)),
        ),
        tooltipTheme: const TooltipThemeData(waitDuration: Duration.zero),
        sliderTheme: const SliderThemeData(
          showValueIndicator: ShowValueIndicator.never,
        ),
      ),
      home: ClipFeedScreen(
        community: community,
        generation: generation,
        initialGeneration: initialGeneration,
        initialPublisher: initialPublisher,
        initialPublicPublisherId: initialPublicPublisherId,
        publisher: publisher,
        initialClipId: initialClipId,
        live: live,
        liveFactory: liveFactory,
        initialLiveId: initialLiveId,
        shareAction: shareAction,
        repository: repository,
        viewerFactory: viewerFactory ?? VideoPreviewAdapter.new,
        sceneFactory: sceneFactory ?? createFreeViewAdapter,
      ),
    );
  }
}
