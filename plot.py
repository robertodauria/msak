#!/usr/bin/env python3

import json
import sys
import os
import matplotlib

import pylab

def main():
    if len(sys.argv) != 3:
        print("Usage: {} <input folder 1> <input folder 2>".format(sys.argv[0]))
        sys.exit(1)

    fig, axes = pylab.subplots(3, 2, sharey="all")
    # Set horizontal spacing
    fig.subplots_adjust(hspace=0.5)

    # Read all folders in each folder passed as argument
    for col, root_dir in enumerate(sys.argv[1:]):
        nstream_dirs = os.listdir(root_dir)
        nstream_dirs.sort()
        # Create a subplot for each folder. 
        for row, nstreams_dir in enumerate(nstream_dirs):
            for i, file in enumerate(os.listdir(os.path.join(root_dir, nstreams_dir))):
                if file.startswith("aggregate"):
                    with open(os.path.join(root_dir, nstreams_dir, file)) as f:
                        data = json.load(f)
                        times = [float(x) for x in data.keys()]
                        axes[row, col].plot(times, data.values(), label="aggregate")
                else: 
                    with open(os.path.join(root_dir, nstreams_dir, file)) as f:
                        data = json.load(f)
                        # Get the x,y values from the data
                        x, y = [], []
                        for measurements in data.get("Download").get("ClientMeasurements"):
                            time = measurements.get("AppInfo").get("ElapsedTime") / 1000000
                            rate = measurements.get("AppInfo").get("NumBytes") / time / 1000000 * 8
                            x.append(time)
                            y.append(rate)

                        axes[row][col].plot(x, y, "x-", label=i)

            # Make sure the ticks are always visible.
            axes[row][col].tick_params(labelleft=True)
            # Set custom step.
            axes[row][col].yaxis.set_major_locator(matplotlib.ticker.MultipleLocator(base=50.0))
            # Set title and labels.
            axes[row][col].set_title("{} - {} streams".format(root_dir, nstreams_dir))
            axes[row][col].set_xlabel("Time (s)")
            axes[row][col].set_ylabel("Throughput (Mbps)")
            # Set legend location.
            axes[row][col].legend(loc="upper left")
            # Enable grid.
            axes[row][col].grid()
                    

    pylab.show()

if __name__ == "__main__":
    main()