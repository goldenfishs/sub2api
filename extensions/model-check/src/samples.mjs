const wrap = (label, body, palette = 'mint', extra = '') => `<!doctype html><html><head><meta charset="UTF-8"><style>
*{box-sizing:border-box}html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#f8faf7}svg{width:100%;height:100%;display:block}text{font-family:system-ui,sans-serif} .float{animation:bob 3s ease-in-out infinite}.spin{transform-box:fill-box;transform-origin:center;animation:spin 3s linear infinite}.breeze{transform-box:fill-box;transform-origin:bottom;animation:sway 3s ease-in-out infinite}@keyframes spin{to{transform:rotate(360deg)}}@keyframes bob{50%{transform:translateY(-10px)}}@keyframes sway{50%{transform:rotate(7deg)}}${extra}</style></head><body><svg viewBox="0 0 960 600" xmlns="http://www.w3.org/2000/svg"><rect width="960" height="600" rx="26" fill="${palette === 'peach' ? '#fcf2e9' : palette === 'lavender' ? '#f1effb' : '#edf6f1'}"/><text x="55" y="61" font-size="13" letter-spacing="4" fill="#6b8276">LUMIVIA · LOCAL STUDY</text><text x="55" y="98" font-size="24" font-weight="600" fill="#304f44">${label}</text>${body}</svg></body></html>`;

export function sampleHTML(topic = 'pelican', weak = false) {
  if (weak) return wrap('Motion study', '<circle cx="480" cy="330" r="44" fill="#bed8ca"/><text x="480" y="410" text-anchor="middle" fill="#608175" font-size="16">Static placeholder</text>');
  if (topic === 'kinetic') return wrap('A little universe', `
    <circle cx="780" cy="100" r="25" fill="#ddd4f4"/>
    <path d="M160 483H800" stroke="#c9c4dc" stroke-width="2"/><ellipse cx="480" cy="492" rx="180" ry="17" fill="#e1dbed"/>
    <path d="M460 380H500L523 468H437Z" fill="#b5aacb"/><rect x="394" y="465" width="172" height="23" rx="10" fill="#746b96"/>
    <ellipse cx="480" cy="295" rx="260" ry="84" fill="none" stroke="#b7a7d8" stroke-width="3" transform="rotate(-20 480 295)"/>
    <ellipse cx="480" cy="295" rx="216" ry="75" fill="none" stroke="#afaad6" stroke-width="2" transform="rotate(29 480 295)"/>
    <circle cx="480" cy="295" r="73" fill="#efc86f"/><circle cx="460" cy="273" r="26" fill="#f9df9b" opacity=".5"/>
    <g><animateTransform attributeName="transform" type="rotate" from="0 480 295" to="360 480 295" dur="6s" repeatCount="indefinite"/><circle cx="670" cy="260" r="27" fill="#b9acd9"/><circle cx="664" cy="252" r="8" fill="#d9cdeb"/></g>
    <g><animateTransform attributeName="transform" type="rotate" from="360 480 295" to="0 480 295" dur="4s" repeatCount="indefinite"/><circle cx="267" cy="310" r="20" fill="#85b9b8"/></g>
    <g class="spin"><path d="M600 448h14v-10h18v10h14v18h-14v10h-18v-10h-14Z" fill="#b7a7d8"/><circle cx="623" cy="457" r="8" fill="#f1effb"/></g>
    <path d="M755 352v18m-9-9h18M230 200v14m-7-7h14" stroke="#c1b4d9" stroke-width="3" stroke-linecap="round"/>
  `, 'lavender');
  if (topic === 'garden' || topic === 'creative') return wrap('Letters from the garden', `
    <circle cx="781" cy="154" r="52" fill="#f2d799"/>
    <path d="M0 437Q160 323 366 430T960 410V600H0Z" fill="#dbe6cb"/><path d="M0 496Q340 418 960 472V600H0Z" fill="#c8d8bb"/>
    <path d="M0 565Q407 461 960 526" fill="none" stroke="#f4e4c7" stroke-width="49"/>
    <g transform="translate(672 268)"><rect x="45" y="90" width="17" height="140" rx="4" fill="#9b886b"/><path d="M0 99Q0 11 58 13Q116 13 116 99Z" fill="#d98677"/><rect x="8" y="94" width="105" height="68" rx="12" fill="#edb79a"/><rect x="31" y="117" width="61" height="9" rx="4" fill="#956459"/><circle cx="27" cy="70" r="10" fill="#ffefcf"/><circle cx="69" cy="40" r="12" fill="#ffefcf"/><circle cx="92" cy="80" r="8" fill="#ffefcf"/></g>
    <g class="float"><path d="M386 350Q334 281 340 264L396 296Q440 264 462 268L450 325Q488 391 451 431L367 433Z" fill="#cf9068"/><path d="M375 335L405 355L438 328Q449 381 411 399Q375 390 375 335Z" fill="#fff1db"/><circle cx="386" cy="328" r="5" fill="#3b5148"/><circle cx="435" cy="325" r="5" fill="#3b5148"/><path d="M402 355h13l-7 8Z" fill="#3b5148"/><path d="M367 387Q312 370 317 426Q332 458 387 437" fill="#cf9068"/><path d="M319 420Q344 432 361 415" stroke="#fff1db" stroke-width="17" fill="none"/>
    <path d="M407 407L432 449M385 412L366 450" stroke="#725d51" stroke-width="10" stroke-linecap="round"/>
    <rect x="431" y="365" width="65" height="46" rx="4" fill="#fef7e6" transform="rotate(-15 461 388)"/><path d="M434 373l31 14 27-27" stroke="#cdbb9e" fill="none" stroke-width="2"/></g>
    <g class="breeze"><path d="M158 485v-66m0 40q-25-30-37-21m37 5q24-31 40-17" stroke="#739e72" stroke-width="5" fill="none"/><circle cx="158" cy="410" r="21" fill="#e0a59b"/><circle cx="158" cy="410" r="8" fill="#f5d888"/></g>
    <g class="breeze"><path d="M827 512v-72" stroke="#739e72" stroke-width="5"/><circle cx="827" cy="433" r="18" fill="#e5ba91"/><circle cx="827" cy="433" r="6" fill="#f5dfad"/></g>
  `, 'peach');
  return wrap('A ride by the sea', `
    <circle cx="786" cy="163" r="55" fill="#f0d583"/>
    <path d="M80 192q15-34 38-10q22-31 45 2q31-2 33 17H72Z" fill="#fffdf3"/><path d="M593 157q10-23 27-7q17-26 39 0h15q15 0 19 16H580Z" fill="#fffdf3"/>
    <path d="M0 358Q170 300 342 367T960 358V600H0Z" fill="#c7ddd1"/><path d="M0 395Q220 350 483 404T960 382V600H0Z" fill="#afd0c3"/><path d="M0 452Q401 417 960 444V600H0Z" fill="#e4dfc6"/>
    <path d="M70 515h90m591-28h61M214 563h84m409-15h100" stroke="#fff8e4" stroke-width="4" stroke-linecap="round"/>
    <g stroke="#537e72" fill="none" stroke-width="6"><circle cx="360" cy="426" r="73"/><circle cx="622" cy="426" r="73"/></g>
    <g stroke="#9bb6a3" stroke-width="2"><g class="spin"><path d="M287 426h146m-73-73v146m-52-125l104 104m-104 0l104-104"/></g><g class="spin"><path d="M549 426h146m-73-73v146m-52-125l104 104m-104 0l104-104"/></g></g>
    <g fill="none" stroke="#c7985f" stroke-width="10" stroke-linejoin="round"><path d="M360 426L422 326L490 426H360L547 340L622 426M490 426L454 324M546 340l-14-44h47"/></g>
    <path d="M410 322h68m84-28h29" stroke="#3e6257" stroke-width="11" stroke-linecap="round"/>
    <g class="float"><path d="M430 242Q383 306 444 334Q492 352 530 315L507 283L489 215" fill="#fffcf1" stroke="#4f796a" stroke-width="4"/><path d="M451 256Q485 230 507 273Q483 307 439 298" fill="#eee9d6" stroke="#779987" stroke-width="3"/>
    <path d="M486 248Q474 224 485 199Q498 171 524 179Q549 191 539 218L528 288" fill="#fffdf2" stroke="#4f796a" stroke-width="4"/>
    <path d="M533 204L670 220Q633 268 537 229Z" fill="#e7b661" stroke="#a7824d" stroke-width="3"/><path d="M536 205L672 220" stroke="#b28d55" stroke-width="3"/>
    <circle cx="523" cy="198" r="5" fill="#365c50"/><circle cx="512" cy="214" r="7" fill="#efc6ad"/>
    <path d="M480 243q37 13 53 3" stroke="#cf997b" stroke-width="12"/><path d="M484 245l-35 21" stroke="#cf997b" stroke-width="10" stroke-linecap="round"/>
    <path d="M473 331L480 373L502 411M500 329L527 363L482 391" stroke="#bc965f" stroke-width="7" fill="none" stroke-linecap="round"/>
    </g><circle cx="490" cy="426" r="10" fill="#527b6b"/>
    <g class="breeze"><path d="M116 466q0-61-23-78m23 57q27-38 40-39" fill="none" stroke="#80a68b" stroke-width="5"/><ellipse cx="93" cy="390" rx="9" ry="23" fill="#a4bb97" transform="rotate(-34 93 390)"/><ellipse cx="154" cy="406" rx="9" ry="21" fill="#a4bb97" transform="rotate(38 154 406)"/></g>
  `);
}
