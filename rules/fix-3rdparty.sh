#!/usr/bin/bash
while read line; do
  fileName="java-$line.yaml"
  name="java-$line"
  echo "Building $fileName ..."
  echo "name: $name" > $fileName 
  echo "filetype: (jsp$|java$)" >> $fileName
  echo "target: line" >> $fileName  
  echo "name: $line" >> $fileName
  echo "type: regex" >> $fileName
  echo "defaultpattern: ^ *import *(io|com|org|net|edu)\.%s" >> $fileName
  echo "advice: Consult 3rd party documentation" >> $fileName
  echo "category: $line" >> $fileName
  echo "tags:" >> $fileName
  echo "- value: $line" >> $fileName
  echo "patterns:" >> $fileName
  echo "- value: $line" >> $fileName
  echo "#- import com.$line" >> $fileName
done < third-party.txt
