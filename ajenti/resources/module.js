angular.module('ajenti.frigotehnica', [])
  .config(['$routeProvider', function ($routeProvider) {
    $routeProvider.when('/view/frigotehnica-tunnel', {
      templateUrl: '/frigotehnica:resources/view.html',
      controller: 'FrigotehnicaTunnelController'
    });
  }])
  .controller('FrigotehnicaTunnelController', ['$scope', 'pageTitle', function ($scope, pageTitle) {
    pageTitle.set('Cloudflare Tunnel');
  }]);

