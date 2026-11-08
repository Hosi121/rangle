#!/usr/bin/env python3
"""Render the rangle presentation architecture without network access.

SVG: Python 3 standard library. PNG: additionally install CairoSVG 2.9.1.
Google product/category icon paths are embedded below from the official library:
https://cloud.google.com/icons (retrieved 2026-11-08).
Google Cloud icons remain Google LLC trademarks; follow the product icon guide.
"""

from argparse import ArgumentParser
from html import escape
from pathlib import Path


ICONS = {'run': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M144.4,272c-6.4,0-12.4-3.8-14.9-10.1L55.4,75.9c-3.3-8.2.7-17.5,8.9-20.8,8.2-3.3,17.5.7,20.8,8.9l74.2,186c3.3,8.2-.7,17.5-8.9,20.8-1.9.8-3.9,1.1-5.9,1.1h-.1Z" fill="#fbbc04" />\n    <g>\n      <path d="M256,272c-6.4,0-12.4-3.8-14.9-10.1l-74.1-186c-2.6-6.6-.6-14.1,5-18.5s13.4-4.5,19.2-.4l260.1,186c7.2,5.1,8.9,15.1,3.7,22.3s-15.1,8.9-22.3,3.7L216.9,114.7l54,135.3c3.3,8.2-.7,17.5-8.9,20.8-1.9.8-4,1.1-5.9,1.1h-.1Z" fill="#ea4335" />\n    </g>\n    <path d="M127.2,256l-72,180c-3.3,8.2.7,17.5,8.9,20.8,3.1,1.2,4,1.1,5.9,1.1,6.3,0,12.4-3.8,14.9-10.1l74.4-186c.8-2,1.1-4,1.1-5.9h-33.2Z" fill="#34a853" />\n    <path d="M414.5,256l-197.7,141.2,54.1-135.3c.8-2,1.1-4,1.1-5.9h-33.2l-72,180c-2.6,6.6-.6,14.1,5,18.5,2.9,2.3,6.4,3.4,9.9,3.4s6.5-1,9.3-3l260.4-186c4.4-3.1,6.7-8,6.7-13h-43.6Z" fill="#4285f4" />\n  </g>\n', 'storage': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M442,277.9H70c-8.8,0-16,7.2-16,16v148.1c0,8.8,7.2,16,16,16h107.2c1.5.5,3.1.7,4.8.7s3.3-.3,4.8-.7h255.2c8.8,0,16-7.2,16-16v-148.1c0-8.8-7.2-16-16-16ZM86,309.9h80v116.1h-80v-116.1ZM426,425.9h-228v-116.1h228v116.1Z" fill="#34a853" />\n    <path d="M442,54H70c-8.8,0-16,7.2-16,16v148.8c0,8.8,7.2,16,16,16h372c8.8,0,16-7.2,16-16V70c0-8.8-7.2-16-16-16ZM86,86h80v116.8h-80v-116.8ZM426,202.8h-228v-116.8h228v116.8Z" fill="#fbbc04" />\n    <path d="M442,234.8h-16V86H54v-16c0-8.8,7.2-16,16-16h372c8.8,0,16,7.2,16,16v148.8c0,8.8-7.2,16-16,16Z" fill="#ea4335" />\n    <path d="M442,457.9h-16v-148.1H54v-16c0-8.8,7.2-16,16-16h372c8.8,0,16,7.2,16,16v148.1c0,8.8-7.2,16-16,16Z" fill="#4285f4" />\n    <circle cx="349" cy="144.4" r="37" fill="#ea4335" />\n    <circle cx="349" cy="367.9" r="37" fill="#4285f4" />\n  </g>\n', 'network': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M331.66,331.66l-22.627-22.627c29.242-29.243,29.242-76.823,0-106.066l22.627-22.627c41.72,41.719,41.72,109.602,0,151.32h0Z" fill="#9aa0a6" />\n    <path d="M180.34,331.66c-41.72-41.719-41.72-109.602,0-151.32l22.627,22.627c-29.242,29.243-29.242,76.823,0,106.066l-22.627,22.627Z" fill="#9aa0a6" />\n    <path d="M309.033,202.967c-29.242-29.242-76.824-29.242-106.066,0l-22.627-22.627c20.21-20.21,47.08-31.34,75.66-31.34s55.45,11.13,75.66,31.34l-22.627,22.627h0Z" fill="#4285f4" />\n    <path d="M256,363c-28.58,0-55.45-11.13-75.66-31.34l22.627-22.627c29.242,29.242,76.824,29.242,106.066,0l22.627,22.627c-20.21,20.21-47.08,31.34-75.66,31.34Z" fill="#4285f4" />\n    <path d="M170.229,147.603c5.406-9.256,8.521-20.007,8.521-31.478,0-34.531-28.094-62.625-62.625-62.625s-62.625,28.094-62.625,62.625,28.094,62.625,62.625,62.625c11.471,0,22.222-3.114,31.478-8.52l45.281,45.281c5.831-9.07,13.557-16.796,22.627-22.627l-45.281-45.281h-.001ZM116.125,146.75c-16.887,0-30.625-13.738-30.625-30.625s13.738-30.625,30.625-30.625,30.625,13.738,30.625,30.625-13.738,30.625-30.625,30.625Z" fill="#4285f4" />\n    <path d="M215.51,319.117c-9.07-5.831-16.796-13.557-22.627-22.627l-45.281,45.281c-9.256-5.406-20.007-8.52-31.478-8.52-34.531,0-62.625,28.094-62.625,62.625s28.094,62.625,62.625,62.625,62.625-28.094,62.625-62.625c0-11.471-3.114-22.222-8.521-31.478l45.281-45.281h0ZM116.125,426.5c-16.887,0-30.625-13.738-30.625-30.625s13.738-30.625,30.625-30.625,30.625,13.738,30.625,30.625-13.738,30.625-30.625,30.625Z" fill="#9aa0a6" />\n    <path d="M395.875,333.25c-11.471,0-22.222,3.114-31.478,8.52l-45.281-45.281c-5.831,9.07-13.557,16.796-22.627,22.627l45.281,45.281c-5.406,9.256-8.521,20.007-8.521,31.478,0,34.531,28.094,62.625,62.625,62.625s62.625-28.094,62.625-62.625-28.094-62.625-62.625-62.625h.001ZM395.875,426.5c-16.887,0-30.625-13.738-30.625-30.625s13.738-30.625,30.625-30.625,30.625,13.738,30.625,30.625-13.738,30.625-30.625,30.625Z" fill="#4285f4" />\n    <path d="M395.875,53.5c-34.531,0-62.625,28.094-62.625,62.625,0,11.645,3.205,22.551,8.764,31.902l-45.118,45.118c9.03,5.887,16.707,13.663,22.481,22.773l45.444-45.444c9.16,5.255,19.758,8.276,31.054,8.276,34.531,0,62.625-28.094,62.625-62.625s-28.094-62.625-62.625-62.625ZM395.875,146.75c-16.887,0-30.625-13.738-30.625-30.625s13.738-30.625,30.625-30.625,30.625,13.738,30.625,30.625-13.738,30.625-30.625,30.625Z" fill="#9aa0a6" />\n  </g>\n', 'identity': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M256.004,287.542c-43.08,0-78.128-35.065-78.128-78.167s35.048-78.167,78.128-78.167,78.129,35.065,78.129,78.167-35.049,78.167-78.129,78.167ZM256.004,163.208c-25.435,0-46.128,20.71-46.128,46.167s20.693,46.167,46.128,46.167,46.129-20.71,46.129-46.167-20.693-46.167-46.129-46.167h0Z" fill="#4285f4" />\n    <path d="M153.505,376.416c-.752,0-1.511-.053-2.276-.162-8.748-1.245-14.83-9.347-13.584-18.096,4.021-28.244,18.11-54.191,39.672-73.062,21.776-19.059,49.72-29.555,78.682-29.555,8.837,0,16,7.163,16,16s-7.163,16-16,16c-21.207,0-41.666,7.684-57.607,21.636-15.801,13.828-26.123,32.825-29.066,53.492-1.137,7.982-7.983,13.746-15.819,13.746h-.002Z" fill="#9aa0a6" />\n    <path d="M358.493,376.427c-7.838,0-14.685-5.764-15.82-13.748-2.94-20.669-13.262-39.668-29.063-53.499-15.941-13.953-36.401-21.638-57.61-21.638-8.837,0-16-7.163-16-16s7.163-16,16-16c28.964,0,56.909,10.497,78.687,29.559,21.562,18.873,35.65,44.823,39.669,73.07,1.244,8.749-4.839,16.85-13.587,18.095-.765.108-1.523.161-2.274.161h-.002Z" fill="#4285f4" />\n    <path d="M256.004,287.542c-43.08,0-78.128-35.065-78.128-78.167s35.048-78.167,78.128-78.167v32c-25.435,0-46.128,20.71-46.128,46.167s20.693,46.167,46.128,46.167v32Z" fill="#9aa0a6" />\n    <path d="M437.95,151.09c-.01-.13-.03-.26-.04-.39-.02-.18-.04-.37-.07-.55-.05-.37-.12-.74-.19-1.1-.08-.36-.17-.72-.27-1.07-.15-.53-.33-1.05-.54-1.56-.06-.15-.12-.3-.18-.44-.23-.55-.5-1.08-.8-1.59-.08-.14-.16-.28-.25-.42-.19-.31-.4-.61-.61-.91-.21-.29-.42-.57-.65-.85l-.02-.02c-.23-.28-.47-.55-.72-.81-.01-.01-.02-.02-.02-.02-.19-.21-.39-.41-.6-.6-.27-.26-.55-.5-.83-.73-.13-.11-.26-.21-.39-.31-.14-.11-.28-.22-.42-.31-.13-.1-.25-.19-.38-.28-.33-.22-.67-.43-1.02-.62-.21-.13-.41-.24-.63-.35-.13-.07-.25-.13-.38-.19l-137.13-68.47-.07-.03-28.59-14.28c-4.17-2.09-9.03-2.24-13.3-.46-.33.14-.66.29-1,.46l-166,82.88c-5.42,2.71-8.85,8.26-8.85,14.32v103.13c0,44.24,21.16,86.5,56.59,113.04l98.72,73.94v.01l17.1,12.8c1.43,1.07,2.99,1.87,4.6,2.39.61.21,1.23.37,1.85.49.78.16,1.57.25,2.35.29.28.01.55.02.8.02.3,0,.59-.01.89-.03.21-.01.41-.02.62-.04.42-.04.85-.1,1.27-.18.25-.04.5-.09.74-.15.07-.01.15-.03.22-.04.01,0,.02-.01.02-.01.24-.06.48-.12.71-.19.26-.07.52-.15.78-.25.3-.11.61-.22.91-.35h.01c.28-.11.55-.24.82-.37.59-.28,1.17-.61,1.73-.99.18-.11.37-.23.54-.36.11-.07.21-.15.32-.23l115.82-86.75c35.43-26.54,56.59-68.8,56.59-113.04v-103.13c0-.44-.02-.87-.05-1.3h0ZM406,255.52c0,34.21-16.37,66.9-43.78,87.43l-106.22,79.56-106.22-79.56c-27.41-20.53-43.78-53.22-43.78-87.43v-93.24l150-74.9,150,74.9v93.24h0Z" fill="#9aa0a6" />\n    <path d="M268.81,452.09c-.94,1.25-2.03,2.33-3.22,3.22-.11.08-.21.16-.32.23-.17.13-.36.25-.54.36-.56.38-1.14.71-1.73.99-.27.13-.54.26-.82.37h-.01c-.3.13-.61.24-.91.35-.26.09-.52.17-.78.25-.23.07-.47.13-.71.19,0,0-.01.01-.02.01-.07.01-.15.03-.22.04-.24.06-.49.11-.74.15-.42.08-.85.14-1.27.18-.21.02-.41.03-.62.04-.3.02-.59.03-.89.03-.25,0-.52-.01-.8-.02-.78-.04-1.57-.13-2.35-.29-.62-.12-1.24-.28-1.85-.49-1.61-.52-3.17-1.32-4.6-2.39l-17.1-12.8v-.01l-98.72-73.94c-35.43-26.54-56.59-68.8-56.59-113.04v-103.13c0-6.06,3.43-11.61,8.85-14.32L248.85,55.19c.34-.17.67-.32,1-.46,4.27-1.78,9.13-1.63,13.3.46l28.59,14.28.07.03-35.81,17.88-150,74.9v93.24c0,34.21,16.37,66.9,43.78,87.43l106.22,79.56,9.59,7.18c7.07,5.3,8.51,15.33,3.22,22.4h0Z" fill="#4285f4" />\n    <path d="M438,152.39v103.13c0,44.24-21.16,86.5-56.59,113.04l-115.82,86.75c-.11.08-.21.16-.32.23-.17.13-.36.25-.54.36-.56.38-1.14.71-1.73.99-.27.13-.54.26-.82.37h-.01c-.3.13-.61.24-.91.35-.26.1-.52.18-.78.25-.23.07-.47.13-.71.19,0,0-.01.01-.02.01-.32.07-.64.14-.96.19-.42.08-.85.14-1.27.18-.21.02-.41.03-.62.04-.3.02-.59.03-.89.03-.25,0-.52-.01-.8-.02-.78-.04-1.57-.13-2.35-.29-.62-.12-1.24-.28-1.85-.49-1.61-.52-3.17-1.32-4.6-2.39l-17.1-12.8v-.01l26.69-19.99,106.22-79.56c27.41-20.53,43.78-53.22,43.78-87.43v-103.13c0-8.84,7.16-16,16-16,2.49,0,4.84.57,6.94,1.58.13.06.25.12.38.19.22.11.42.22.63.35.35.19.69.4,1.02.62.13.09.25.18.38.28.14.09.28.2.42.31.13.1.26.2.39.31.28.23.56.47.83.73.21.19.41.39.6.6,0,0,.01.01.02.02.25.26.49.53.72.81l.02.02c.23.28.44.56.65.85.21.3.42.6.61.91.09.14.17.28.25.42.3.51.57,1.04.8,1.59.06.14.12.29.18.44.21.51.39,1.03.54,1.56.1.35.19.71.27,1.07.07.36.14.73.19,1.1.03.18.05.37.07.55.01.13.03.26.04.39.03.43.05.86.05,1.3h0Z" fill="#9aa0a6" />\n  </g>\n', 'devops': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M349,272h-15.5c-8.837,0-16-7.163-16-16s7.163-16,16-16h15.5c42.458,0,77-34.542,77-77s-34.542-77-77-77c-8.837,0-16-7.163-16-16s7.163-16,16-16c60.103,0,109,48.897,109,109s-48.897,109-109,109Z" fill="#9aa0a6" />\n    <path d="M163,458c-60.103,0-109-48.897-109-109s48.897-109,109-109h15.5c8.837,0,16,7.163,16,16s-7.163,16-16,16h-15.5c-42.458,0-77,34.542-77,77s34.542,77,77,77c8.837,0,16,7.163,16,16s-7.163,16-16,16Z" fill="#9aa0a6" />\n    <path d="M163,458v-32c42.458,0,77-34.542,77-77v-186c0-60.103,48.897-109,109-109v32c-42.458,0-77,34.542-77,77v186c0,60.103-48.897,109-109,109Z" fill="#4285f4" />\n  </g>\n', 'developer': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M180.63,347.41c-4.09,0-8.19-1.56-11.31-4.68-6.25-6.25-6.25-16.38,0-22.63l138.65-138.68c6.25-6.25,16.38-6.25,22.63,0s6.25,16.38,0,22.63l-138.65,138.68c-3.12,3.12-7.22,4.69-11.32,4.69v-.01Z" fill="#9aa0a6" />\n    <path d="M134.65,458.35c-44.66,0-81-36.34-81-81s36.34-81,81-81,81,36.34,81,81-36.34,81-81,81ZM134.65,328.35c-27.02,0-49,21.98-49,49s21.98,49,49,49,49-21.98,49-49-21.98-49-49-49Z" fill="#4285f4" />\n    <path d="M380.81,234.22c-27.52,0-53.39-10.72-72.85-30.18s-30.18-45.33-30.18-72.85,10.72-53.39,30.18-72.85c6.25-6.25,16.38-6.25,22.63,0s6.25,16.38,0,22.63c-27.69,27.7-27.69,72.76,0,100.45,27.7,27.7,72.76,27.69,100.45,0,6.25-6.25,16.38-6.25,22.63,0s6.25,16.38,0,22.63c-19.46,19.46-45.33,30.18-72.85,30.18l-.01-.01Z" fill="#4285f4" />\n    <path d="M106.92,234.73c-4.1,0-8.19-1.56-11.31-4.69-6.25-6.25-6.25-16.38,0-22.63l63.23-63.23-63.23-63.23c-6.25-6.25-6.25-16.38,0-22.63s16.38-6.25,22.63,0l74.54,74.54c3,3,4.69,7.07,4.69,11.31s-1.69,8.31-4.69,11.31l-74.54,74.54c-3.12,3.12-7.22,4.69-11.31,4.69l-.01.02Z" fill="#9aa0a6" />\n    <path d="M405.08,458.35c-4.1,0-8.19-1.56-11.31-4.69l-74.54-74.54c-3-3-4.69-7.07-4.69-11.31s1.69-8.31,4.69-11.31l74.54-74.54c6.25-6.25,16.38-6.25,22.63,0s6.25,16.38,0,22.63l-63.23,63.23,63.23,63.23c6.25,6.25,6.25,16.38,0,22.63-3.12,3.12-7.22,4.69-11.31,4.69l-.01-.02Z" fill="#9aa0a6" />\n    <path d="M192.77,132.88l-11.31-11.31-85.85,85.85c-6.25,6.25-6.25,16.38,0,22.63,3.12,3.12,7.22,4.69,11.31,4.69s8.19-1.56,11.31-4.69l74.54-74.54c6.25-6.25,6.25-16.38,0-22.63Z" fill="#4285f4" />\n    <path d="M319.23,379.12l11.31,11.31,85.85-85.85c6.25-6.25,6.25-16.38,0-22.63-3.12-3.12-7.22-4.69-11.31-4.69s-8.19,1.56-11.31,4.69l-74.54,74.54c-6.25,6.25-6.25,16.38,0,22.63Z" fill="#4285f4" />\n  </g>\n', 'integration': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M346.683,112.611l-.039-75.171c-.005-8.834-7.167-15.992-16-15.992h-.008c-8.837.004-15.997,7.172-15.992,16.008l.039,75.155H118c-17.673,0-32,14.327-32,32v111.865c0,89.085,76.262,161.562,170,161.562s170-72.477,170-161.562v-111.865c0-17.673-14.327-32-32-32h-47.317ZM394,256.477c0,71.44-61.906,129.562-138,129.562s-138-58.121-138-129.562v-111.865h276v111.865Z" fill="#4285f4" />\n    <path d="M314.644,144.611v-32h-117.294l-.039-75.171c-.005-8.834-7.167-15.992-16-15.992h-.008c-8.837.004-15.997,7.172-15.992,16.008l.039,75.155h-47.35c-17.673,0-32,14.327-32,32v111.865c0,83.959,67.741,153.155,154.007,160.841v56.679c0,8.616,6.621,16.029,15.228,16.433,9.188.432,16.772-6.889,16.772-15.982v-89.277c-6.594.627-8.014.652-16,.867h-.007c-76.094,0-138-58.121-138-129.562v-111.865h196.644,0Z" fill="#9aa0a6" />\n  </g>\n', 'analytics': '<g>\n    <rect width="512" height="512" fill="none" />\n  </g>\n  <g>\n    <path d="M155.24,255.523c-11.135,0-21.995,1.79-32.254,5.226l-38.85-74.169c-4.096-7.817-13.754-10.834-21.568-6.741-7.817,4.095-10.835,13.752-6.741,21.568l38.865,74.199c-3.868,2.899-7.558,6.09-11.037,9.569-19.122,19.12-29.652,44.544-29.652,71.585s10.531,52.464,29.652,71.585c19.12,19.121,44.543,29.652,71.585,29.652s52.465-10.531,71.585-29.652c39.466-39.473,39.469-103.7,0-143.17-19.12-19.121-44.544-29.652-71.585-29.652h0ZM204.229,405.749c-13.086,13.086-30.484,20.292-48.989,20.292s-35.903-7.206-48.989-20.292-20.292-30.484-20.292-48.989,7.206-35.903,20.292-48.989c5-5,10.633-9.133,16.713-12.342.039-.02.079-.037.119-.058.009-.005.016-.01.025-.015,9.796-5.141,20.752-7.877,32.132-7.877,18.506,0,35.903,7.207,48.989,20.292,13.085,13.086,20.292,30.483,20.292,48.989s-7.207,35.903-20.292,48.989h0Z" fill="#9aa0a6" />\n    <path d="M456.172,310.593l-38.865-74.198c3.868-2.899,7.559-6.09,11.038-9.57,19.122-19.12,29.652-44.544,29.652-71.585s-10.53-52.464-29.652-71.585c-19.12-19.12-44.545-29.652-71.585-29.652s-52.464,10.531-71.585,29.652c-39.472,39.472-39.472,103.698,0,143.17,19.12,19.121,44.544,29.652,71.585,29.652,11.136,0,21.995-1.79,32.255-5.226l38.85,74.169c2.854,5.45,8.413,8.567,14.167,8.567,2.499,0,5.034-.587,7.4-1.828,7.818-4.094,10.835-13.751,6.74-21.567h0ZM307.771,204.229c-13.084-13.086-20.292-30.483-20.292-48.989s7.207-35.903,20.292-48.989c13.086-13.086,30.484-20.292,48.989-20.292s35.903,7.206,48.989,20.292,20.292,30.484,20.292,48.989-7.206,35.903-20.292,48.989c-5.031,5.03-10.702,9.182-16.825,12.399-.002,0-.004.002-.006.003-.001,0-.002.001-.003.002-9.802,5.149-20.766,7.888-32.154,7.888-18.506,0-35.903-7.207-48.989-20.292h-.001Z" fill="#9aa0a6" />\n    <path d="M356.76,54.003c-27.041,0-52.465,10.531-71.585,29.652-19.121,19.12-29.652,44.544-29.652,71.585,0,21.638,6.749,42.235,19.24,59.401l-83.135,83.135c5.337,3.522,6.499,4.277,12.601,9.995,27.879,26.118,27.012,70.965,0,97.978-13.086,13.086-30.484,20.292-48.989,20.292s-35.903-7.206-48.989-20.292l-22.596,22.596c19.12,19.121,44.544,29.652,71.585,29.652s52.465-10.531,71.585-29.652c19.121-19.12,29.652-44.544,29.652-71.585,0-21.638-6.749-42.235-19.24-59.401l83.134-83.134c-3.873-2.619-6.619-4.107-12.6-9.996-27.222-26.801-27.012-70.965,0-97.978,13.086-13.086,30.484-20.292,48.989-20.292s35.903,7.206,48.989,20.292l22.596-22.596c-19.12-19.121-44.544-29.652-71.585-29.652h0Z" fill="#4285f4" />\n  </g>\n'}

INK = "#203044"
MUTED = "#56687b"
BLUE = "#246cce"
GREEN = "#16815b"
CONTROL = "#8190a2"
parts = []


def add(markup):
    parts.append(markup)


def rect(x, y, w, h, fill="white", stroke="#d4dfe9", radius=12, width=1.5):
    add(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{radius}" '
        f'fill="{fill}" stroke="{stroke}" stroke-width="{width}"/>')


def label(x, y, content, size=20, color=INK, weight=400, anchor="start", mono=False):
    family = "DejaVu Sans Mono, monospace" if mono else "Ubuntu Sans, DejaVu Sans, sans-serif"
    add(f'<text x="{x}" y="{y}" font-family="{family}" font-size="{size}" '
        f'font-weight="{weight}" fill="{color}" text-anchor="{anchor}">{escape(content)}</text>')


def icon(name, x, y, size=48):
    add(f'<svg x="{x}" y="{y}" width="{size}" height="{size}" viewBox="0 0 512 512">'
        + ICONS[name] + '</svg>')


def line(d, color=BLUE, dashed=False, arrow=True, width=3):
    dash = ' stroke-dasharray="6 6"' if dashed else ''
    marker = f' marker-end="url(#{color[1:]})"' if arrow else ''
    add(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}" '
        f'stroke-linejoin="round" stroke-linecap="round"{dash}{marker}/>')


def pill(x, y, w, content, color=BLUE, fill="#edf4ff", size=17):
    rect(x, y, w, 29, fill=fill, stroke="none", radius=7)
    label(x + w / 2, y + 20, content, size=size, color=color, weight=600, anchor="middle")


def render():
    parts.clear()
    add('<?xml version="1.0" encoding="UTF-8"?>')
    add('<svg xmlns="http://www.w3.org/2000/svg" width="1920" height="1080" '
        'viewBox="0 0 1920 1080" role="img" aria-labelledby="title description">')
    add('<title id="title">rangle cloud architecture — presentation staging</title>')
    add('<desc id="description">Browsers resolve app.rangleplay.stream and media.rangleplay.stream '
        'through DNS-only Cloudflare records and connect directly to a Google global HTTPS load '
        'balancer. The app uses a serverless NEG and Cloud Run in Tokyo, reading a private catalog '
        'through read-only Cloud Storage FUSE. Media uses Cloud CDN with a private Cloud Storage '
        'origin, bypassing Go. GitHub Actions deploys with Workload Identity Federation. A scheduled '
        'workflow checks budget messages and the deadline to stop owned serving resources.</desc>')
    add('<defs>')
    for color in [BLUE, GREEN, CONTROL]:
        add(f'<marker id="{color[1:]}" viewBox="0 0 10 10" refX="9" refY="5" '
            f'markerWidth="7" markerHeight="7" orient="auto-start-reverse">'
            f'<path d="M 1 1 L 9 5 L 1 9" fill="none" stroke="{color}" '
            'stroke-width="1.7" stroke-linejoin="round"/></marker>')
    add('</defs>')
    rect(0, 0, 1920, 1080, fill="#ffffff", stroke="none", radius=0)

    # Heading and restrained path legend.
    label(64, 68, "rangle", size=46, weight=700)
    label(223, 68, "/ Cloud architecture", size=38, weight=400)
    label(65, 110, "Application and media delivery on Google Cloud", size=24, color=MUTED)
    label(1856, 63, "PRESENTATION STAGING", size=18, color=MUTED, weight=600, anchor="end")
    label(1856, 99, "08 NOV 2026", size=18, color=MUTED, anchor="end")
    line("M 64 132 H 1856", color="#8190a2", arrow=False, width=1)

    # Cloud and geographic scopes. Cloudflare is deliberately outside the HTTP path.
    rect(358, 164, 1498, 500, fill="#f8fafc", stroke="#c7d5e3", radius=18)
    label(386, 206, "Google Cloud", size=26, weight=600)
    label(568, 206, "project · comin-up", size=20, color=MUTED)
    line("M 822 198 H 860", color=BLUE, arrow=False)
    label(871, 205, "App / API", size=17, color=MUTED)
    line("M 1001 198 H 1039", color=GREEN, arrow=False)
    label(1050, 205, "Media", size=17, color=MUTED)
    label(386, 258, "GLOBAL EDGE", size=16, color=MUTED, weight=600)
    # The regional boundary includes the serverless NEG, but excludes the CDN.
    add('<path d="M 802 230 H 1814 Q 1828 230 1828 244 V 621 Q 1828 635 1814 635 '
        'H 1128 Q 1114 635 1114 621 V 436 Q 1114 422 1100 422 H 802 '
        'Q 788 422 788 408 V 244 Q 788 230 802 230 Z" '
        'fill="#f0f5fb" stroke="#c7d5e3" stroke-width="1.5"/>')
    label(814, 266, "TOKYO", size=16, color=MUTED, weight=600)
    label(900, 266, "asia-northeast1", size=20, color=MUTED)

    # DNS resolution is shown as a separate, dashed relationship.
    rect(64, 174, 235, 111, fill="#fafbfc", stroke="#d4dfe9")
    label(84, 211, "Cloudflare DNS", size=23, weight=600)
    label(84, 242, "DNS only · proxy off", size=18, color=MUTED)
    label(84, 268, "A records to LB IPv4", size=17, color=MUTED)
    line("M 181 350 V 285", color=CONTROL, dashed=True, width=2)
    label(196, 326, "resolve", size=17, color=MUTED)

    # Main request paths; service cards are drawn on top of connector endpoints.
    line("M 299 439 H 406", color=BLUE)
    label(352, 419, "HTTPS", size=17, color=BLUE, anchor="middle")
    line("M 715 380 H 759 V 348 H 801", color=BLUE)
    line("M 1027 348 H 1144", color=BLUE)
    line("M 1464 386 H 1537", color=BLUE)
    line("M 715 500 H 759 V 554 H 801", color=GREEN)
    line("M 1027 554 H 1537", color=GREEN)
    pill(729, 307, 57, "app")
    pill(723, 578, 75, "media", color=GREEN, fill="#e9f5ef")
    label(1277, 580, "cache fill on miss · CDN identity", size=17, color=GREEN, anchor="middle")
    label(1277, 607, "Large media goes directly through the CDN", size=18, color=GREEN, anchor="middle")

    # Browser, including an intentionally generic browser glyph.
    rect(64, 350, 235, 178)
    rect(87, 372, 59, 43, fill="#f5f8fc", stroke="#7890a9", radius=4)
    line("M 88 384 H 145", color=CONTROL, arrow=False, width=1.5)
    for cx in [95, 103, 111]:
        add(f'<circle cx="{cx}" cy="378" r="1.6" fill="{CONTROL}"/>')
    label(84, 452, "Web browser", size=25, weight=600)
    label(84, 483, "Flutter Web UI", size=20, color=MUTED)
    label(84, 509, "Fetches app + media", size=17, color=MUTED)

    # One global external ALB routes both registered hostnames.
    rect(406, 326, 309, 222)
    icon("network", 428, 346, 47)
    label(485, 376, "HTTPS load balancer", size=22, weight=600)
    label(430, 418, "Global external · TLS + host routing", size=17, color=MUTED)
    pill(429, 441, 263, "app.rangleplay.stream", size=17)
    pill(429, 479, 263, "media.rangleplay.stream", color=GREEN, fill="#e9f5ef", size=17)
    label(430, 530, "Single global IPv4 · HTTP to HTTPS", size=16, color=MUTED)

    rect(801, 302, 226, 92)
    icon("network", 815, 316, 38)
    label(860, 342, "Serverless NEG", size=21, weight=600)
    label(819, 376, "App backend · CDN off", size=18, color=MUTED)

    rect(1144, 302, 320, 190)
    icon("run", 1163, 316, 55)
    label(1227, 352, "Cloud Run", size=27, weight=600)
    label(1168, 390, "Go + Flutter Web assets", size=21, color=MUTED)
    label(1168, 423, "/", size=17, color=BLUE, mono=True)
    label(1280, 423, "landing", size=18, color=MUTED)
    label(1168, 450, "/demo/", size=17, color=BLUE, mono=True)
    label(1280, 450, "web UI", size=18, color=MUTED)
    label(1168, 477, "/api/v1/clips", size=17, color=BLUE, mono=True)
    label(1147, 516, "1 vCPU · 512 MiB · concurrency 40", size=17, color=MUTED)
    label(1147, 539, "min 0 / max 1 instance · request billing", size=16, color=MUTED)

    rect(1537, 302, 265, 190)
    icon("storage", 1555, 317, 53)
    label(1616, 352, "Catalog", size=25, weight=600)
    label(1558, 391, "Private Cloud Storage", size=20, color=MUTED)
    label(1558, 424, "Read-only FUSE mount", size=18, color=BLUE)
    label(1558, 452, "snapshots/<sha>.json", size=16, color=MUTED, mono=True)
    label(1558, 477, "Runtime SA · objectViewer", size=16, color=MUTED)

    rect(801, 506, 226, 96)
    icon("network", 815, 518, 40)
    label(865, 548, "Cloud CDN", size=24, weight=600)
    label(819, 582, "Immutable · TTL 24 h", size=18, color=MUTED)

    rect(1537, 506, 265, 96)
    icon("storage", 1552, 521, 44)
    label(1607, 545, "Media", size=25, weight=600)
    label(1607, 572, "Private GCS origin", size=18, color=MUTED)
    label(1558, 591, "PAP enforced · no allUsers", size=15, color=MUTED)
    label(1138, 629, "Separate private buckets: inputs / weights + Terraform state", size=17, color=MUTED)
    label(65, 579, "PUBLIC DOMAIN", size=16, color=MUTED, weight=600)
    label(65, 610, "rangleplay.stream", size=20, color=MUTED)
    label(65, 639, "App + media share the LB", size=17, color=MUTED)

    # Supporting control-plane lanes.
    label(64, 699, "DEPLOYMENT & OPERATIONS", size=16, color=MUTED, weight=600)
    rect(64, 715, 1037, 288, fill="#fafbfc", stroke="#d4dfe9", radius=14)
    rect(1125, 715, 731, 288, fill="#fafbfc", stroke="#d4dfe9", radius=14)
    label(88, 754, "Keyless deployment", size=25, weight=600)
    label(1075, 752, "stg branch / dedicated identity", size=17, color=MUTED, anchor="end")

    # CI → WIF → dedicated SA; this SA can publish images and deploy revisions.
    for path in ["M 262 850 H 293", "M 463 850 H 496", "M 710 850 H 739 V 816 H 768", "M 739 850 V 916 H 768"]:
        line(path, color=CONTROL, dashed=True, width=2)
    rect(88, 792, 174, 115)
    label(106, 838, "GitHub Actions", size=21, weight=600)
    label(106, 878, "stg workflow", size=18, color=MUTED)
    rect(293, 792, 170, 115)
    icon("identity", 307, 807, 38)
    label(353, 836, "WIF", size=24, weight=600)
    label(308, 878, "OIDC · short-lived", size=17, color=MUTED)
    rect(496, 792, 214, 115)
    icon("identity", 510, 807, 38)
    label(555, 836, "Deployer SA", size=21, weight=600)
    label(511, 878, "Dedicated identity", size=18, color=MUTED)
    rect(768, 780, 307, 72)
    icon("devops", 783, 794, 39)
    label(833, 810, "Artifact Registry", size=21, weight=600)
    label(833, 836, "Immutable image digest", size=17, color=MUTED)
    rect(768, 880, 307, 72)
    icon("run", 783, 893, 40)
    label(833, 910, "Cloud Run revisions", size=21, weight=600)
    label(833, 936, "Candidate / smoke / promote", size=17, color=MUTED)
    label(88, 949, "CI owns image + traffic", size=19, color=MUTED)
    label(88, 980, "Terraform owns infra / IAM / catalog / mount / scaling", size=19, color=MUTED)

    label(1149, 754, "Budget & deadline guard", size=25, weight=600)
    label(1149, 785, "JPY 15,000 or 14 Nov 2026 · 15:00 UTC", size=20, color=MUTED)
    line("M 1308 844 H 1339", color=CONTROL, dashed=True, width=2)
    line("M 1508 844 H 1541 V 874 H 1578", color=CONTROL, dashed=True, width=2)
    line("M 1389 938 H 1541 V 906 H 1578", color=CONTROL, dashed=True, width=2)
    label(1542, 826, "pull", size=16, color=MUTED, anchor="middle")
    label(1471, 927, "every 15 min", size=17, color=MUTED, anchor="middle")
    rect(1149, 813, 159, 63)
    label(1163, 838, "Billing budget", size=19, weight=600)
    label(1163, 863, "JPY 30,000", size=17, color=MUTED)
    rect(1339, 813, 169, 63)
    icon("analytics", 1350, 827, 34)
    label(1392, 849, "Pub/Sub", size=21, weight=600)
    rect(1149, 906, 240, 63)
    icon("developer", 1160, 919, 35)
    label(1205, 944, "Cloud Scheduler", size=21, weight=600)
    rect(1578, 837, 252, 94)
    icon("integration", 1592, 850, 42)
    label(1646, 872, "Workflows", size=24, weight=600)
    label(1597, 910, "Budget + deadline check", size=18, color=MUTED)
    label(1149, 989, "Stop owned Run + LB entry · disable deploy SA", size=19, color=MUTED)

    # Keep claims and the current small staging capacity explicit.
    label(64, 1045, "USD 300 authorized · Billing notifications are not a hard cap", size=20, color=MUTED)
    label(1856, 1045, "Large-scale capacity remains unvalidated", size=20, color=MUTED, anchor="end")
    add('</svg>')
    return '\n'.join(parts) + '\n'


if __name__ == "__main__":
    parser = ArgumentParser(description=__doc__)
    parser.add_argument("--png", action="store_true", help="also export 3840×2160 PNG using CairoSVG")
    args = parser.parse_args()
    output = Path(__file__).with_suffix(".svg")
    svg = render()
    output.write_text(svg, encoding="utf-8")
    print(output)
    if args.png:
        import cairosvg
        png = output.with_suffix(".png")
        cairosvg.svg2png(bytestring=svg.encode("utf-8"), write_to=str(png), output_width=3840, output_height=2160)
        print(png)
