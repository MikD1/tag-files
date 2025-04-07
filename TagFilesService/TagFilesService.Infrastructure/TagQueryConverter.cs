using System.Text;
using System.Text.RegularExpressions;

namespace TagFilesService.Infrastructure;

public static class TagQueryConverter
{
    public static string ConvertToDynamicQuery(string input)
    {
        const string pattern = @"(!)?([a-zA-Z0-9\-]+)|([()])|(&&)|(\|\|)";
        MatchCollection matches = Regex.Matches(input, pattern);

        StringBuilder builder = new();
        foreach (Match match in matches)
        {
            if (!string.IsNullOrEmpty(match.Groups[2].Value))
            {
                string not = match.Groups[1].Success ? "!" : string.Empty;
                string tag = match.Groups[2].Value;
                builder.Append($"{not}Tags.Any(Name==\"{tag}\")");
            }
            else if (match.Value is "&&" or "||" or "(" or ")")
            {
                builder.Append($"{match.Value}");
            }
        }

        return builder.ToString().Trim();
    }
}