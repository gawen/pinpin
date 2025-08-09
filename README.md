# Pinpin

🇫🇷

Pinpin est un client **non officiel** pour l’enceinte **Merlin**, développé en
Go. Il permet de téléverser directement sur l’enceinte vos **fichiers audio
personnels** (musique, histoires, podcasts libres de droits, etc.).

Nécessite :
- ffmpeg

## Utilisation

```bash
# Créer un dossier qui contiendra votre arborescence de fichiers Pinpin.
$ mkdir mes_fichiers_pinpin
$ cd mes_fichiers_pinpin

# Créer les dossiers qui apparaîtront dans le Merlin.
$ mkdir Historias
$ cd Historias

# Placez-y vos histoires, dans un format (audio ou vidéo) que ffmpeg peut lire.
# Le nom des fichiers sera le nom des histoires qui apparaîtront dans le
# Merlin.
$ ls
Gato.mp3
Hormiga.webm
Mariposa.mp4
Mariquita.webm
Perro.mp3

# Enfin, exécutez Pinpin et suivez les instructions.
$ cd ../../
$ go run ./path/to/pinpin/cmd mes_fichiers_pinpin
```

## Légal

Veuillez lire le fichier [`DISCLAIMER.md`](DISCLAIMER.md).
