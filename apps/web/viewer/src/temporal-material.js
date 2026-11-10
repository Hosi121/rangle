import { DataTexture, FloatType, RGBAFormat, NearestFilter } from 'three';
import { MOTION_TEXTURE_WIDTH } from './temporal-format.js';

const centreAnchor = 'vec3 splatCenter = uintBitsToFloat(uvec3(sampledCenterColor.gba));';
const colorAnchor = 'vColor = uintToRGBAVec(sampledCenterColor.r);';

/** Extend the pinned public ShaderMaterial once; each displayed time changes a uniform. */
export function attachTemporalMaterial(material, model) {
  for (const anchor of [centreAnchor, colorAnchor]) {
    if (material.vertexShader.split(anchor).length !== 2) throw new Error('Temporal shader integration changed');
  }
  const texture = new DataTexture(model.texture, MOTION_TEXTURE_WIDTH, model.textureHeight, RGBAFormat, FloatType);
  texture.minFilter = texture.magFilter = NearestFilter;
  texture.generateMipmaps = false;
  texture.needsUpdate = true;
  material.uniforms.rangleTime = { value: 0 };
  material.uniforms.rangleMotion = { value: texture };
  material.uniforms.rangleTextureWidth = { value: MOTION_TEXTURE_WIDTH };
  const centre = [
    centreAnchor,
    'int rgIndex = int(splatIndex) * 2;',
    'ivec2 rgUV = ivec2(rgIndex % rangleTextureWidth, rgIndex / rangleTextureWidth);',
    'vec4 rgMotion = texelFetch(rangleMotion, rgUV, 0);',
    'vec4 rgLife = texelFetch(rangleMotion, rgUV + ivec2(1, 0), 0);',
    'float rgDelta = rangleTime - rgMotion.w;',
    'float rgRatio = rgDelta / rgLife.x;',
    'float rgOpacity = rgLife.y * exp(-0.5 * rgRatio * rgRatio);',
    'float rgAlpha = roundEven(255.0 * rgOpacity) / 255.0;',
    'if (rgAlpha < (1.0 / 255.0)) { gl_Position = vec4(0.0, 0.0, 2.0, 1.0); return; }',
    'splatCenter += rgMotion.xyz * rgDelta;',
  ].join('\n');
  material.vertexShader = [
    'uniform highp sampler2D rangleMotion;',
    'uniform highp float rangleTime;',
    'uniform int rangleTextureWidth;',
    material.vertexShader.replace(centreAnchor, centre).replace(colorAnchor, colorAnchor + '\nvColor.a = rgAlpha;'),
  ].join('\n');
  material.needsUpdate = true;
  return { setTime(time) { material.uniforms.rangleTime.value = time; }, dispose() { texture.dispose(); } };
}
