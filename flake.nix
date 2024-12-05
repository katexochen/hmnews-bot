# Based on https://github.com/nix-community/home-manager/blob/65912bc6841cf420eb8c0a20e03df7cbbff5963f/home-manager/home-manager#L441-L469
{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixpkgs-unstable";
    home-manager.url = "github:nix-community/home-manager";
    home-manager.inputs.nixpkgs.follows = "nixpkgs";
  };
  outputs =
    { nixpkgs, home-manager, ... }:
    let
      pkgs = nixpkgs.legacyPackages.x86_64-linux;
    in
    {
      homeConfigurations.a = home-manager.lib.homeManagerConfiguration {
        inherit pkgs;
        modules = [
          {
            home.username = "a";
            home.homeDirectory = "/dev/null";
            home.stateVersion = "24.05";
          }
        ];
      };
    };
}
