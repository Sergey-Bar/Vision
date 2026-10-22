// The service worker, registered.
//
// It used to be five inline lines in every page, and the Content Security
// Policy the console server sends has `script-src 'self'` and no
// `unsafe-inline`: an inline script is blocked, and a blocked script is a
// console error on every page of the demo.
//
// One file, linked by every page, is also one place to look when the
// registration stops working.
if ('serviceWorker' in navigator) {
  window.addEventListener('load', function () {
    navigator.serviceWorker.register('/static/sw.js').catch(function () {
      // A service worker that will not register is not a reason to break
      // the page, and the page works without one.
    })
  })
}
